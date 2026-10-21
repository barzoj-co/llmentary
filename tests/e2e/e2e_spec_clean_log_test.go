// SPDX-License-Identifier: AGPL-3.0-only

//go:build e2e

package llmentary_test

import (
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/barzoj-co/llmentary/internal/proxy"
)

func TestE2ESpecCleanLogged(t *testing.T) {
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
	body := []byte(`{"model":` + quoteJSON(liveModel()) + `,"input":"Reply with the single word pong.","stream":true,"max_output_tokens":32}`)
	status, _, resp, _ := postLive(t, apiKey, p, "/responses", sid, body)
	if status != 200 {
		t.Fatalf("status=%d body=%s", status, redactLiveBody(resp))
	}
	var spec string
	for _, line := range strings.Split(logBuf.String(), "\n") {
		if !strings.Contains(line, `msg="proxy response"`) {
			continue
		}
		for _, field := range strings.Fields(line) {
			if strings.HasPrefix(field, "spec=") {
				spec = strings.TrimPrefix(field, "spec=")
			}
		}
	}
	if spec == "clean" {
		return
	}
	if strings.HasPrefix(spec, "findings:") {
		t.Fatalf("expected spec=clean, got %s; findings log:\n%s", spec, logBuf.String())
	}
	t.Fatalf("missing spec=clean on proxy response (got %q)\n%s", spec, logBuf.String())
}
