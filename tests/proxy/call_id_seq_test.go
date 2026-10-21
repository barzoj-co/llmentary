// SPDX-License-Identifier: AGPL-3.0-only

package proxy_test

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/barzoj-co/llmentary/internal/proxy"
)

func TestCallIDProcessGlobalSeqPerSession(t *testing.T) {
	logBuf := &syncLogBuf{}
	p := startSpecProxy(t, "off", "relaxed", chatOK(), func(cfg *proxy.Config) {
		cfg.Logger = slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	})
	body := []byte(`{"model":"gpt-fixture","messages":[{"role":"user","content":"hi"}]}`)
	postRaw(t, p, "/v1/chat/completions", "alpha", nil, body)
	postRaw(t, p, "/v1/chat/completions", "beta", nil, body)
	postRaw(t, p, "/v1/chat/completions", "alpha", nil, body)

	alpha := waitSession(t, p.controlURL, "alpha")
	beta := waitSession(t, p.controlURL, "beta")
	aCalls, _ := alpha["calls"].([]any)
	bCalls, _ := beta["calls"].([]any)
	if len(aCalls) != 2 || len(bCalls) != 1 {
		t.Fatalf("alpha=%d beta=%d", len(aCalls), len(bCalls))
	}
	ids := map[string]bool{}
	for i, raw := range aCalls {
		c, _ := raw.(map[string]any)
		id := fmtString(c["id"])
		if ids[id] {
			t.Fatalf("duplicate id %s", id)
		}
		ids[id] = true
		if intFrom(c["seq"]) != i+1 {
			t.Fatalf("alpha seq=%v want %d", c["seq"], i+1)
		}
	}
	bc, _ := bCalls[0].(map[string]any)
	if ids[fmtString(bc["id"])] {
		t.Fatalf("beta id reused: %v", bc["id"])
	}
	if intFrom(bc["seq"]) != 1 {
		t.Fatalf("beta seq=%v", bc["seq"])
	}
	logs := logBuf.String()
	if !strings.Contains(logs, "seq=1") || !strings.Contains(logs, "seq=2") {
		t.Fatalf("logs missing seq: %s", logs)
	}
	var reqSeq, respSeq int
	for _, line := range strings.Split(logs, "\n") {
		if strings.Contains(line, `msg="proxy request"`) && strings.Contains(line, "seq=") {
			reqSeq++
		}
		if strings.Contains(line, `msg="proxy response"`) && strings.Contains(line, "seq=") {
			respSeq++
		}
	}
	if reqSeq != 3 || respSeq != 3 {
		t.Fatalf("seq fields request=%d response=%d log=%s", reqSeq, respSeq, logs)
	}
}
