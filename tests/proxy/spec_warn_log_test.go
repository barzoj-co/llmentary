// SPDX-License-Identifier: AGPL-3.0-only

package proxy_test

import (
	"bytes"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/barzoj-co/llmentary/internal/proxy"
)

type syncLogBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncLogBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncLogBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func warnLines(log string) []string {
	var out []string
	for _, line := range strings.Split(log, "\n") {
		if strings.Contains(line, "level=WARN") {
			out = append(out, line)
		}
	}
	return out
}

func TestP1SpecWarnLoggedUnknownField(t *testing.T) {
	logBuf := &syncLogBuf{}
	const prompt = "UNIQUE_PROMPT_XYZ"
	const fieldVal = "UNIQUE_FIELD_VALUE"
	const headerVal = "UNIQUE_HEADER_SECRET"
	p := startSpecProxy(t, "warn", "relaxed", chatOK(), func(cfg *proxy.Config) {
		cfg.Logger = slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	})
	body := []byte(`{"model":"gpt-fixture","messages":[{"role":"user","content":"` + prompt + `"}],"foo":"` + fieldVal + `"}`)
	sid := newSession()
	postRaw(t, p, "/v1/chat/completions", sid, map[string]string{
		"Authorization": "Bearer " + headerVal,
		"X-Custom-Foo":  headerVal,
	}, body)
	lines := warnLines(logBuf.String())
	if len(lines) != 2 {
		t.Fatalf("WARN lines=%d want 2\n%s", len(lines), logBuf.String())
	}
	if !strings.Contains(lines[0], `msg="spec violations"`) {
		t.Fatalf("summary=%s", lines[0])
	}
	if !strings.Contains(lines[0], "side=request") || !strings.Contains(lines[0], "count=1") {
		t.Fatalf("summary fields: %s", lines[0])
	}
	if !strings.Contains(lines[0], `first="/foo unevaluatedProperties"`) {
		t.Fatalf("summary first: %s", lines[0])
	}
	if !strings.Contains(lines[1], `msg="spec violation"`) {
		t.Fatalf("detail=%s", lines[1])
	}
	if !strings.Contains(lines[1], "pointer=/foo") || !strings.Contains(lines[1], "keyword=unevaluatedProperties") {
		t.Fatalf("detail fields: %s", lines[1])
	}
	for i, line := range lines {
		for _, secret := range []string{prompt, fieldVal, headerVal} {
			if strings.Contains(line, secret) {
				t.Fatalf("WARN[%d] leaked %q: %s", i, secret, line)
			}
		}
	}
}

func TestP2CleanOffAndSummaryOnly(t *testing.T) {
	t.Run("clean_no_warn", func(t *testing.T) {
		logBuf := &syncLogBuf{}
		p := startSpecProxy(t, "warn", "relaxed", chatOK(), func(cfg *proxy.Config) {
			cfg.Logger = slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))
		})
		postRaw(t, p, "/v1/chat/completions", newSession(), nil, []byte(`{"model":"gpt-fixture","messages":[{"role":"user","content":"hi"}]}`))
		if lines := warnLines(logBuf.String()); len(lines) != 0 {
			t.Fatalf("clean request logged WARN: %v", lines)
		}
	})
	t.Run("off_no_warn", func(t *testing.T) {
		logBuf := &syncLogBuf{}
		p := startSpecProxy(t, "off", "relaxed", chatOK(), func(cfg *proxy.Config) {
			cfg.Logger = slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))
		})
		postRaw(t, p, "/v1/chat/completions", newSession(), nil, []byte(`{"model":"gpt-fixture","messages":[{"role":"user","content":"hi"}],"foo":1}`))
		if lines := warnLines(logBuf.String()); len(lines) != 0 {
			t.Fatalf("off mode logged WARN: %v", lines)
		}
	})
	t.Run("spec_log_max_zero_summary_only", func(t *testing.T) {
		logBuf := &syncLogBuf{}
		p := startSpecProxy(t, "warn", "relaxed", chatOK(), func(cfg *proxy.Config) {
			cfg.Logger = slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))
			cfg.SpecLogMax = proxy.IntPtr(0)
		})
		postRaw(t, p, "/v1/chat/completions", newSession(), nil, []byte(`{"model":"gpt-fixture","messages":[{"role":"user","content":"hi"}],"foo":1}`))
		lines := warnLines(logBuf.String())
		if len(lines) != 1 {
			t.Fatalf("WARN lines=%d want 1\n%s", len(lines), logBuf.String())
		}
		if !strings.Contains(lines[0], `msg="spec violations"`) {
			t.Fatalf("summary=%s", lines[0])
		}
		if strings.Contains(logBuf.String(), `msg="spec violation"`) {
			t.Fatalf("expected summary only, got per-finding line: %s", logBuf.String())
		}
	})
}

func TestP3IgnoredFieldsWarn(t *testing.T) {
	logBuf := &syncLogBuf{}
	p := startSpecProxy(t, "warn", "relaxed", chatOK(), func(cfg *proxy.Config) {
		cfg.Logger = slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	})
	body := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"shirt","parameters":{"type":"object","properties":{"color":{"type":"string","enum":["red","blue"],"description":"Shirt color","enumDescriptions":{"red":"warm tone","blue":"cool tone"}}},"required":["color"]}}}]}`)
	postRaw(t, p, "/v1/chat/completions", newSession(), nil, body)
	lines := warnLines(logBuf.String())
	var summary, detail string
	for _, line := range lines {
		if strings.Contains(line, `msg="ignored fields"`) {
			summary = line
		}
		if strings.Contains(line, `msg="ignored field"`) {
			detail = line
		}
	}
	if summary == "" || detail == "" {
		t.Fatalf("missing ignored-fields WARN lines: %v", lines)
	}
	if !strings.Contains(detail, "kind=unknown_schema_keyword") {
		t.Fatalf("detail kind: %s", detail)
	}
	if !strings.Contains(detail, "enumDescriptions") {
		t.Fatalf("detail path/pointer: %s", detail)
	}
	for i, line := range lines {
		if strings.Contains(line, "warm tone") || strings.Contains(line, "cool tone") {
			t.Fatalf("WARN[%d] leaked enum description value: %s", i, line)
		}
	}
}

func TestEnforceRejectLogsWarn(t *testing.T) {
	logBuf := &syncLogBuf{}
	p := startSpecProxy(t, "enforce", "relaxed", chatOK(), func(cfg *proxy.Config) {
		cfg.Logger = slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	})
	status, _, _, _ := postRaw(t, p, "/v1/chat/completions", newSession(), nil, []byte(`{"model":"gpt-fixture","messages":[{"role":"user","content":"hi"}],"foo":1}`))
	if status != 400 {
		t.Fatalf("status=%d want 400", status)
	}
	if p.upstream.Count() != 0 {
		t.Fatal("enforce reject should not call upstream")
	}
	lines := warnLines(logBuf.String())
	if len(lines) != 2 {
		t.Fatalf("WARN lines=%d want 2\n%s", len(lines), logBuf.String())
	}
	if !strings.Contains(lines[0], `msg="spec violations"`) || !strings.Contains(lines[0], "mode=enforce") {
		t.Fatalf("summary=%s", lines[0])
	}
	if !strings.Contains(lines[1], "pointer=/foo") || !strings.Contains(lines[1], "keyword=unevaluatedProperties") {
		t.Fatalf("detail=%s", lines[1])
	}
}

func TestP4FindingsTableInDocs(t *testing.T) {
	files := []string{
		filepath.Join("..", "..", "docs", "proxy.md"),
		filepath.Join("..", "..", "docs", "data-model.md"),
	}
	needles := []string{
		"Where findings show up",
		"`validation`",
		"`spec_violations`",
		"`ignored_fields`",
		"`[]` means checked and clean",
		"Absent means not checked",
	}
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		for _, n := range needles {
			if !strings.Contains(text, n) {
				t.Errorf("%s missing %q", path, n)
			}
		}
	}
}

func TestSpecLogMaxInvalidCLI(t *testing.T) {
	cmd := exec.Command(proxyBinary(t), "--spec-log-max=-1", "--upstream", "https://example.com")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected non-zero exit")
	}
	if !bytes.Contains(out, []byte("--spec-log-max")) {
		t.Fatalf("message=%s", out)
	}
}
