package lab

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/barzoj-co/llm-test-lab/pkg/llmtestlab"
)

func TestProxyPassesStreamUnchangedAndRecordsToolCall(t *testing.T) {
	stream := []byte("data: {\"id\":\"chatcmpl_1\",\"model\":\"gpt-test\",\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"lookup\",\"arguments\":\"{\\\"city\\\"\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\":\\\"Paris\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":9,\"completion_tokens\":4,\"total_tokens\":13}}\n\n" +
		"data: [DONE]\n\n")
	var sawAuth, sawSessionHeader bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization") == "Bearer test-token"
		sawSessionHeader = r.Header.Get(llmtestlab.SessionHeaderName) != ""
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("x-request-id", "req_fake")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write(stream); err != nil {
			t.Errorf("write stream: %v", err)
		}
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	}))
	defer upstream.Close()

	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Upstreams = nil
	if err := cfg.AddUpstream("default=" + upstream.URL); err != nil {
		t.Fatal(err)
	}
	store := NewMemoryStore()
	server, err := NewServer(cfg, store, TolerantParser{})
	if err != nil {
		t.Fatal(err)
	}
	proxy := httptest.NewServer(server.ProxyHandler())
	defer proxy.Close()
	control := httptest.NewServer(server.ControlHandler())
	defer control.Close()
	client, err := llmtestlab.NewClient(control.URL)
	if err != nil {
		t.Fatal(err)
	}
	sessionID := client.NewSession()

	reqBody := []byte(`{"model":"gpt-test","messages":[{"role":"user","content":"hi"}],"stream":true,"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}]}`)
	req, err := http.NewRequest(http.MethodPost, proxy.URL+"/v1/chat/completions", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set(llmtestlab.SessionHeaderName, sessionID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, stream) {
		t.Fatalf("stream mutated:\n got=%q\nwant=%q", got, stream)
	}
	if !sawAuth {
		t.Fatalf("authorization header was not forwarded")
	}
	if sawSessionHeader {
		t.Fatalf("session header leaked upstream")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	calls, err := client.Wait(ctx, sessionID, 10*time.Millisecond, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	llmtestlab.AssertCallCount(t, calls, 1)
	llmtestlab.AssertToolCalled(t, calls, "lookup", map[string]any{"city": "Paris"})
	llmtestlab.AssertToolOrder(t, calls, "lookup")
	llmtestlab.AssertToolNotCalled(t, calls, "other")
	llmtestlab.AssertTokensBelow(t, calls, 20)
	if got := calls[0].Request.Headers["Authorization"][0]; got == "Bearer test-token" || got == "" {
		t.Fatalf("authorization header not redacted: %q", got)
	}
	if calls[0].RequestIDs["x-request-id"] != "req_fake" {
		t.Fatalf("request id not recorded: %+v", calls[0].RequestIDs)
	}
}
