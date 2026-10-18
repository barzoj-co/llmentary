package llmtestlab_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/barzoj-co/llm-test-lab/internal/lab"
	"github.com/barzoj-co/llm-test-lab/pkg/llmtestlab"
)

func TestFixtureProxyAssertions(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		contentType string
		requestPath string
		requestBody string
		assert      func(*testing.T, *llmtestlab.Assertions)
	}{
		{
			name: "chat stream tool",
			path: "chat_stream_tool.sse", contentType: "text/event-stream",
			requestPath: "/v1/chat/completions", requestBody: `{"model":"gpt-fixture","stream":true,"messages":[{"role":"user","content":"weather"}]}`,
			assert: func(t *testing.T, a *llmtestlab.Assertions) {
				chat := a.Trace().Query().APIType("chat").Expect(t)
				chat.ToolCalled("get_weather", llmtestlab.ArgsSubset(map[string]any{"city": "Paris"}))
				chat.FinishReason("tool_calls")
				chat.TokensAtMost(20)
				chat.NoErrors()
			},
		},
		{
			name: "chat nonstream tool",
			path: "chat_nonstream_tool.json", contentType: "application/json",
			requestPath: "/v1/chat/completions", requestBody: `{"model":"gpt-fixture","stream":false}`,
			assert: func(t *testing.T, a *llmtestlab.Assertions) {
				a.ToolCalled("get_weather", llmtestlab.ArgPath("unit", "celsius"))
				a.FinishReason("tool_calls")
				a.TokensAtMost(20)
			},
		},
		{
			name: "responses stream tool",
			path: "responses_stream_tool.sse", contentType: "text/event-stream",
			requestPath: "/v1/responses", requestBody: `{"model":"gpt-fixture","stream":true,"input":"weather"}`,
			assert: func(t *testing.T, a *llmtestlab.Assertions) {
				responses := a.Trace().Query().APIType("responses").Expect(t)
				responses.ToolCalled("get_weather", llmtestlab.ArgsSubset(map[string]any{"city": "Paris"}))
				responses.FinishReason("tool_calls")
				responses.TokensAtMost(20)
				responses.NoErrors()
			},
		},
		{
			name: "responses nonstream tool",
			path: "responses_nonstream_tool.json", contentType: "application/json",
			requestPath: "/v1/responses", requestBody: `{"model":"gpt-fixture","input":"weather"}`,
			assert: func(t *testing.T, a *llmtestlab.Assertions) {
				a.ToolCalled("get_weather", llmtestlab.ArgPath("unit", "celsius"))
				a.FinishReason("tool_calls")
				a.TokensAtMost(25)
			},
		},
		{
			name: "chat midstream error",
			path: "chat_stream_midstream_error.sse", contentType: "text/event-stream",
			requestPath: "/v1/chat/completions", requestBody: `{"model":"gpt-fixture","stream":true}`,
			assert: func(t *testing.T, a *llmtestlab.Assertions) {
				a.HasError()
				a.MidStreamError()
			},
		},
		{
			name: "azure empty choices filter",
			path: "azure_content_filter_empty_choices.sse", contentType: "text/event-stream",
			requestPath: "/openai/deployments/dep/chat/completions?api-version=2024-10-21", requestBody: `{"stream":true}`,
			assert: func(t *testing.T, a *llmtestlab.Assertions) {
				a.ToolCalled("get_weather", llmtestlab.ArgsSubset(map[string]any{"city": "Paris"}))
				a.FinishReason("tool_calls")
				a.NoErrors()
			},
		},
		{
			name: "structured output",
			path: "structured_output.json", contentType: "application/json",
			requestPath: "/v1/chat/completions", requestBody: `{"model":"gpt-fixture","response_format":{"type":"json_object"}}`,
			assert: func(t *testing.T, a *llmtestlab.Assertions) {
				a.ResponseJSONValid()
				a.ResponseJSONSchemaValid(map[string]any{
					"type":     "object",
					"required": []string{"answer", "count"},
					"properties": map[string]any{
						"answer": map[string]any{"type": "string"},
						"count":  map[string]any{"type": "integer"},
					},
				})
				a.FinishReason("stop")
				a.TokensAtMost(20)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join("testdata", tt.path))
			if err != nil {
				t.Fatal(err)
			}
			fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tt.contentType)
				_, _ = w.Write(body)
			}))
			defer fake.Close()
			labClient, proxy := newFixtureLab(t, fake.URL)
			req, err := http.NewRequest(http.MethodPost, proxy+tt.requestPath, bytes.NewBufferString(tt.requestBody))
			if err != nil {
				t.Fatal(err)
			}
			key, value := labClient.Header()
			req.Header.Set(key, value)
			req.Header.Set("x-stainless-retry-count", "1")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			assert := labClient.Expect(ctx)
			assert.Retries(1)
			tt.assert(t, assert)
		})
	}
}

func newFixtureLab(t *testing.T, upstream string) (*llmtestlab.Lab, string) {
	t.Helper()
	cfg := lab.Config{ListenAddr: ":0", ControlAddr: "127.0.0.1:0"}
	if err := cfg.AddUpstream("default=" + upstream); err != nil {
		t.Fatal(err)
	}
	server, err := lab.NewServer(cfg, lab.NewMemoryStore(), lab.TolerantParser{})
	if err != nil {
		t.Fatal(err)
	}
	proxy := httptest.NewServer(server.ProxyHandler())
	t.Cleanup(proxy.Close)
	control := httptest.NewServer(server.ControlHandler())
	t.Cleanup(control.Close)
	return llmtestlab.New(t, llmtestlab.WithProxyURL(proxy.URL), llmtestlab.WithControlURL(control.URL), llmtestlab.WithWait(time.Millisecond, time.Second)), proxy.URL
}
