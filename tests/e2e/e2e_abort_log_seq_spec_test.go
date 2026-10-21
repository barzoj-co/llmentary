// SPDX-License-Identifier: AGPL-3.0-only

//go:build e2e

package llmentary_test

import (
	"context"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/barzoj-co/llmentary/internal/proxy"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/responses"
)

func TestE2EAbortLogHasSeqAndSpec(t *testing.T) {
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
	client := openai.NewClient(
		option.WithAPIKey(apiKey),
		option.WithBaseURL(p.proxyURL+"/s/"+sid+"/v1"),
		option.WithMaxRetries(0),
	)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	stream := client.Responses.NewStreaming(ctx, responses.ResponseNewParams{
		Model: liveModel(),
		Input: responses.ResponseNewParamsInputUnion{
			OfString: openai.String("Write a very long bedtime story with many chapters, at least two thousand words."),
		},
	})
	for stream.Next() {
	}
	_ = stream.Err()
	_ = stream.Close()
	_ = waitLiveControl(t, p.controlURL, sid)

	var n int
	for _, line := range strings.Split(logBuf.String(), "\n") {
		if strings.Contains(line, `msg="proxy response"`) && strings.Contains(line, "client_aborted=true") {
			n++
			for _, want := range []string{"seq=1", "spec=request_only", "phase=before_headers"} {
				if !strings.Contains(line, want) {
					t.Fatalf("abort log missing %s: %s\nfull:\n%s", want, line, logBuf.String())
				}
			}
		}
	}
	if n != 1 {
		t.Fatalf("proxy response abort lines=%d log=%s", n, logBuf.String())
	}
}
