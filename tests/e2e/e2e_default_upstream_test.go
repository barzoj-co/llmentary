// SPDX-License-Identifier: AGPL-3.0-only

//go:build e2e

package llmentary_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/barzoj-co/llmentary/internal/proxy"
	"github.com/barzoj-co/llmentary/internal/tracetest"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/param"
)

func TestE2EDefaultUpstream(t *testing.T) {
	apiKey := requireAPIKey(t)
	model := e2eModel()
	for _, tc := range []struct {
		name  string
		start func(*testing.T) *tracetest.Session
	}{
		{name: "no_upstream_flag", start: newE2EDefault},
		{name: "explicit_v1_upstream", start: newE2E},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lm := tc.start(t)
			client := openai.NewClient(
				option.WithAPIKey(apiKey),
				option.WithBaseURL(lm.BaseURL("")+"/v1"),
				option.WithHTTPClient(lm.HTTPClient()),
				option.WithMaxRetries(0),
			)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			stream := client.Chat.Completions.NewStreaming(ctx, openai.ChatCompletionNewParams{
				Model:       model,
				Temperature: param.NewOpt(0.0),
				MaxTokens:   param.NewOpt(int64(16)),
				Messages: []openai.ChatCompletionMessageParamUnion{
					openai.UserMessage("Reply with the single word pong."),
				},
				StreamOptions: openai.ChatCompletionStreamOptionsParam{
					IncludeUsage: openai.Bool(true),
				},
			})
			var sawChunk bool
			for stream.Next() {
				sawChunk = true
			}
			if err := stream.Err(); err != nil {
				t.Fatal(err)
			}
			_ = stream.Close()
			if !sawChunk {
				t.Fatal("expected streaming chunks")
			}
			trace := lm.Wait(ctx)
			chat := trace.Query().APIType("chat").Calls()
			assertCallCount(t, chat, 1)
			if chat[0].HTTPStatus != 200 {
				t.Fatalf("http_status=%d", chat[0].HTTPStatus)
			}
			if chat[0].Normalized.Usage == nil || chat[0].Normalized.Usage.TotalTokens == nil {
				t.Fatalf("expected usage on chat call: %+v", chat[0].Normalized.Usage)
			}
			assertNoErrors(t, chat)
			assertNoParserWarnings(t, trace.Calls)
		})
	}
}

func newE2EDefault(t *testing.T) *tracetest.Session {
	t.Helper()
	cfg, err := proxy.ConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Upstreams = nil
	server, err := proxy.NewServer(cfg, proxy.NewMemoryStore(), proxy.TolerantParser{})
	if err != nil {
		t.Fatal(err)
	}
	proxySrv := httptest.NewServer(server.ProxyHandler())
	t.Cleanup(proxySrv.Close)
	control := httptest.NewServer(server.ControlHandler())
	t.Cleanup(control.Close)
	return tracetest.New(t, tracetest.WithProxyURL(proxySrv.URL), tracetest.WithControlURL(control.URL), tracetest.WithWait(100*time.Millisecond, 5*time.Second))
}
