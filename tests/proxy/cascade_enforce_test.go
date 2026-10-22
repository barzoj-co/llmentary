// SPDX-License-Identifier: AGPL-3.0-only

package proxy_test

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/barzoj-co/llmentary/internal/proxy"
)

func TestEnforceBlamesToolChoiceNotMaxTokens(t *testing.T) {
	p := startSpecProxy(t, "enforce", "relaxed", chatOK())
	body := []byte(`{"model":"m","max_tokens":16,"messages":[{"role":"user","content":"hi"}],"tool_choice":"any","tools":[{"type":"function","function":{"name":"x","parameters":{"type":"object"}}}]}`)
	status, _, raw, sess := postRaw(t, p, "/v1/chat/completions", newSession(), nil, body)
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", status, raw)
	}
	var env map[string]any
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	errObj, _ := env["error"].(map[string]any)
	msg := fmtString(errObj["message"])
	if strings.Contains(msg, "/max_tokens") || fmtString(errObj["param"]) == "max_tokens" {
		t.Fatalf("blamed max_tokens: %s", raw)
	}
	if !strings.Contains(msg, "tool_choice") && !strings.Contains(fmtString(errObj["param"]), "tool_choice") {
		t.Fatalf("expected tool_choice in enforce error: %s", raw)
	}
	call := lastCall(t, sess)
	n := normalized(call)
	if first := firstSpecFinding(n); strings.Contains(first, "max_tokens") {
		t.Fatalf("trace first=%s", first)
	}
}

func TestEnforceBlamesMessageExtraNotMaxTokensStrict(t *testing.T) {
	p := startSpecProxy(t, "enforce", "strict", chatOK())
	body := []byte(`{"model":"m","max_tokens":16,"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"x","foo":1}]}`)
	status, _, raw, _ := postRaw(t, p, "/v1/chat/completions", newSession(), nil, body)
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", status, raw)
	}
	if strings.Contains(string(raw), "/max_tokens") {
		t.Fatalf("blamed max_tokens: %s", raw)
	}
	if !strings.Contains(string(raw), "/messages/1/foo") && !strings.Contains(string(raw), "messages[1].foo") {
		t.Fatalf("expected message extra: %s", raw)
	}
}

func TestEnforceWarnFirstMatchesTrace(t *testing.T) {
	logBuf := &syncLogBuf{}
	p := startSpecProxy(t, "warn", "relaxed", chatOK(), func(cfg *proxy.Config) {
		cfg.Logger = slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	})
	body := []byte(`{"model":"m","max_tokens":16,"messages":[{"role":"user","content":"hi"}],"tool_choice":"any","tools":[{"type":"function","function":{"name":"x","parameters":{"type":"object"}}}]}`)
	_, _, _, sess := postRaw(t, p, "/v1/chat/completions", newSession(), nil, body)
	first := firstSpecFinding(normalized(lastCall(t, sess)))
	var logFirst string
	for _, line := range strings.Split(logBuf.String(), "\n") {
		if !strings.Contains(line, `msg="spec violations"`) {
			continue
		}
		const prefix = `first="`
		i := strings.Index(line, prefix)
		if i < 0 {
			continue
		}
		rest := line[i+len(prefix):]
		if j := strings.Index(rest, `"`); j >= 0 {
			logFirst = rest[:j]
		}
	}
	if first == "" || logFirst == "" {
		t.Fatalf("first=%q logFirst=%q log=%s", first, logFirst, logBuf.String())
	}
	if strings.Contains(first, "max_tokens") || strings.Contains(logFirst, "max_tokens") {
		t.Fatalf("first=%q logFirst=%q", first, logFirst)
	}
	if logFirst != first {
		t.Fatalf("log first=%q trace first=%q", logFirst, first)
	}
}

func firstSpecFinding(n map[string]any) string {
	for _, v := range asAnySlice(n["spec_violations"]) {
		m, _ := v.(map[string]any)
		if fmtString(m["side"]) == "response" {
			continue
		}
		return fmtString(m["pointer"]) + " " + fmtString(m["keyword"])
	}
	return ""
}
