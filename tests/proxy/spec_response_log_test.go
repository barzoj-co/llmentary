// SPDX-License-Identifier: AGPL-3.0-only

package proxy_test

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/barzoj-co/llmentary/internal/proxy"
)

func proxyResponseSpec(log string) string {
	for _, line := range strings.Split(log, "\n") {
		if !strings.Contains(line, `msg="proxy response"`) {
			continue
		}
		for _, field := range strings.Fields(line) {
			if strings.HasPrefix(field, "spec=") {
				return strings.TrimPrefix(field, "spec=")
			}
		}
	}
	return ""
}

func TestSpecLogClean(t *testing.T) {
	logBuf := &syncLogBuf{}
	p := startSpecProxy(t, "warn", "relaxed", chatOK(), func(cfg *proxy.Config) {
		cfg.Logger = slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	})
	postRaw(t, p, "/v1/chat/completions", newSession(), nil, []byte(`{"model":"gpt-fixture","messages":[{"role":"user","content":"hi"}]}`))
	if spec := proxyResponseSpec(logBuf.String()); spec != "clean" {
		t.Fatalf("spec=%q log=%s", spec, logBuf.String())
	}
	if lines := warnLines(logBuf.String()); len(lines) != 0 {
		t.Fatalf("clean WARN: %v", lines)
	}
}

func TestSpecLogOffAndSkipped(t *testing.T) {
	t.Run("off", func(t *testing.T) {
		logBuf := &syncLogBuf{}
		p := startSpecProxy(t, "off", "relaxed", chatOK(), func(cfg *proxy.Config) {
			cfg.Logger = slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))
		})
		postRaw(t, p, "/v1/chat/completions", newSession(), nil, []byte(`{"model":"gpt-fixture","messages":[{"role":"user","content":"hi"}]}`))
		if spec := proxyResponseSpec(logBuf.String()); spec != "off" {
			t.Fatalf("spec=%q log=%s", spec, logBuf.String())
		}
	})
	t.Run("skipped_max_bytes", func(t *testing.T) {
		logBuf := &syncLogBuf{}
		p := startSpecProxy(t, "warn", "relaxed", chatOK(), func(cfg *proxy.Config) {
			cfg.SpecValidationMaxBytes = 8
			cfg.Logger = slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))
		})
		postRaw(t, p, "/v1/chat/completions", newSession(), nil, []byte(`{"model":"gpt-fixture","messages":[{"role":"user","content":"hi"}]}`))
		spec := proxyResponseSpec(logBuf.String())
		if spec != "skipped:body_too_large" {
			t.Fatalf("spec=%q log=%s", spec, logBuf.String())
		}
	})
}

func TestSpecLogFindings(t *testing.T) {
	logBuf := &syncLogBuf{}
	p := startSpecProxy(t, "warn", "relaxed", responsesOK(), func(cfg *proxy.Config) {
		cfg.Logger = slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	})
	postRaw(t, p, "/v1/responses", newSession(), nil, []byte(`{"model":"gpt-fixture","input":"hi","foo":1}`))
	spec := proxyResponseSpec(logBuf.String())
	if !strings.HasPrefix(spec, "findings:") {
		t.Fatalf("spec=%q log=%s", spec, logBuf.String())
	}
	lines := warnLines(logBuf.String())
	if len(lines) < 2 {
		t.Fatalf("WARN lines=%d want DOG-3 summary+detail\n%s", len(lines), logBuf.String())
	}
	if !strings.Contains(lines[0], `msg="spec violations"`) {
		t.Fatalf("summary=%s", lines[0])
	}
	if !strings.Contains(lines[1], `msg="spec violation"`) || !strings.Contains(lines[1], "pointer=/foo") {
		t.Fatalf("detail=%s", lines[1])
	}
}

func TestSpecValidationStartupLog(t *testing.T) {
	logBuf := &syncLogBuf{}
	_ = startSpecProxy(t, "warn", "relaxed", responsesOK(), func(cfg *proxy.Config) {
		cfg.Logger = slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	})
	logs := logBuf.String()
	if !strings.Contains(logs, `msg="spec validation"`) {
		t.Fatalf("missing startup line: %s", logs)
	}
	if !strings.Contains(logs, "mode=warn") || !strings.Contains(logs, "profile=relaxed") {
		t.Fatalf("startup fields: %s", logs)
	}
	if !strings.Contains(logs, "spec_sha=") || !strings.Contains(logs, "max_bytes=") {
		t.Fatalf("startup sha/max: %s", logs)
	}
}
