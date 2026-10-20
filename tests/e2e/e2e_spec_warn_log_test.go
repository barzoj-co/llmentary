// SPDX-License-Identifier: AGPL-3.0-only

//go:build e2e

package llmentary_test

import (
	"bytes"
	"log/slog"
	"net/http/httptest"
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

func TestLiveSpecWarnLogged(t *testing.T) {
	apiKey := requireAPIKey(t)
	logBuf := &syncLogBuf{}
	cfg := proxy.Config{
		ListenAddr:     ":0",
		ControlAddr:    "127.0.0.1:0",
		SpecValidation: "warn",
		SpecProfile:    "relaxed",
		Logger:         slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelInfo})),
	}
	if err := cfg.AddUpstream("openai=https://api.openai.com/v1"); err != nil {
		t.Fatal(err)
	}
	server, err := proxy.NewServer(cfg, proxy.NewMemoryStore(), proxy.TolerantParser{})
	if err != nil {
		t.Fatal(err)
	}
	proxySrv := httptest.NewServer(server.ProxyHandler())
	t.Cleanup(proxySrv.Close)
	control := httptest.NewServer(server.ControlHandler())
	t.Cleanup(control.Close)
	p := liveProxy{proxyURL: proxySrv.URL, controlURL: control.URL}

	sid := liveSession()
	body := []byte(`{"model":` + quoteJSON(liveModel()) + `,"messages":[{"role":"user","content":"Reply with ok."}],"max_tokens":16,"foo":1}`)
	status, _, resp, _ := postLive(t, apiKey, p, "/chat/completions", sid, body)
	if status != 400 {
		t.Fatalf("status=%d body=%s", status, redactLiveBody(resp))
	}
	logs := logBuf.String()
	if !strings.Contains(logs, `msg="spec violations"`) {
		t.Fatalf("missing spec violations WARN: %s", logs)
	}
	if !strings.Contains(logs, "pointer=/foo") || !strings.Contains(logs, "keyword=unevaluatedProperties") {
		t.Fatalf("missing per-violation WARN: %s", logs)
	}
	if strings.Contains(logs, apiKey) {
		t.Fatal("proxy log leaked API key")
	}
}
