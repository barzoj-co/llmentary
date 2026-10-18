// SPDX-License-Identifier: AGPL-3.0-only

package proxy_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/barzoj-co/llm-test-lab/internal/lab"
	"github.com/barzoj-co/llm-test-lab/internal/tracetest"
)

func TestProxyPassesStreamUnchangedAndRecordsToolCall(t *testing.T) {
	stream := []byte("data: {\"id\":\"chatcmpl_1\",\"model\":\"gpt-test\",\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"lookup\",\"arguments\":\"{\\\"city\\\"\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\":\\\"Paris\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":9,\"completion_tokens\":4,\"total_tokens\":13}}\n\n" +
		"data: [DONE]\n\n")
	var sawAuth, sawSessionHeader, sawOtherBaggage, sawLLMTestLabBaggage bool
	var upstreamTraceparent string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization") == "Bearer test-token"
		sawSessionHeader = r.Header.Get(tracetest.SessionHeaderName) != ""
		sawOtherBaggage = r.Header.Get("baggage") == "other=value"
		sawLLMTestLabBaggage = bytes.Contains([]byte(r.Header.Get("baggage")), []byte("llmtestlab."))
		upstreamTraceparent = r.Header.Get("traceparent")
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

	cfg, err := lab.ConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Upstreams = nil
	if err := cfg.AddUpstream("openai=" + upstream.URL); err != nil {
		t.Fatal(err)
	}
	store := lab.NewMemoryStore()
	server, err := lab.NewServer(cfg, store, lab.TolerantParser{})
	if err != nil {
		t.Fatal(err)
	}
	proxy := httptest.NewServer(server.ProxyHandler())
	defer proxy.Close()
	control := httptest.NewServer(server.ControlHandler())
	defer control.Close()
	client, err := tracetest.NewClient(control.URL)
	if err != nil {
		t.Fatal(err)
	}
	sessionID := client.NewSession()

	reqBody := []byte(`{"model":"gpt-test","messages":[{"role":"user","content":"hi"}],"stream":true,"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}]}`)
	req, err := http.NewRequest(http.MethodPost, proxy.URL+"/u/openai/v1/chat/completions", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set(tracetest.SessionHeaderName, sessionID)
	req.Header.Set("baggage", "llmtestlab.session.id=from-baggage, other=value")
	req.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-00")
	req.Header.Set("x-stainless-retry-count", "1")
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
	if !sawOtherBaggage || sawLLMTestLabBaggage {
		t.Fatalf("baggage not stripped correctly")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	calls, err := client.Wait(ctx, sessionID, 10*time.Millisecond, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	assertCallCount(t, calls, 1)
	assertToolCalled(t, calls, "lookup", map[string]any{"city": "Paris"})
	assertToolOrder(t, calls, "lookup")
	assertToolNotCalled(t, calls, "other")
	assertTokensBelow(t, calls, 20)
	if got := calls[0].Request.Headers["Authorization"][0]; got == "Bearer test-token" || got == "" {
		t.Fatalf("authorization header not redacted: %q", got)
	}
	if calls[0].RequestIDs["x-request-id"] != "req_fake" {
		t.Fatalf("request id not recorded: %+v", calls[0].RequestIDs)
	}

	rawOTLP, err := client.OTLP(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	var otlp map[string]any
	if err := json.Unmarshal(rawOTLP, &otlp); err != nil {
		t.Fatal(err)
	}
	span := onlySpan(t, otlp)
	if span["traceId"] != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("trace id = %v", span["traceId"])
	}
	if span["parentSpanId"] != "00f067aa0ba902b7" {
		t.Fatalf("parent span id = %v", span["parentSpanId"])
	}
	spanID, _ := span["spanId"].(string)
	if upstreamTraceparent == "" || !bytes.Contains([]byte(upstreamTraceparent), []byte(spanID)) {
		t.Fatalf("upstream traceparent %q does not contain proxy span id %s", upstreamTraceparent, spanID)
	}
	attrs := attrsByKey(span)
	assertStringAttr(t, attrs, "gen_ai.operation.name", "chat")
	assertStringAttr(t, attrs, "gen_ai.provider.name", "openai")
	assertStringAttr(t, attrs, "gen_ai.request.model", "gpt-test")
	assertStringAttr(t, attrs, "openai.api.type", "chat_completions")
	assertStringAttr(t, attrs, "llmtestlab.session.id", sessionID)
	assertStringAttr(t, attrs, "llmtestlab.session.source", "header")
	assertIntAttr(t, attrs, "http.request.resend_count", "1")
	assertStringArrayAttr(t, attrs, "llmtestlab.tool_calls.names", []string{"lookup"})

	if err := client.Delete(ctx, sessionID); err != nil {
		t.Fatal(err)
	}
	rawOTLP, err = client.OTLP(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if got := spanCount(t, rawOTLP); got != 0 {
		t.Fatalf("spans after delete = %d, want 0", got)
	}
}

func onlySpan(t *testing.T, traces map[string]any) map[string]any {
	t.Helper()
	resourceSpans := asSlice(t, traces["resourceSpans"], "resourceSpans")
	scopeSpans := asSlice(t, asMap(t, resourceSpans[0], "resourceSpans[0]")["scopeSpans"], "scopeSpans")
	spans := asSlice(t, asMap(t, scopeSpans[0], "scopeSpans[0]")["spans"], "spans")
	if len(resourceSpans) != 1 || len(scopeSpans) != 1 || len(spans) != 1 {
		t.Fatalf("unexpected otlp shape: %+v", traces)
	}
	return asMap(t, spans[0], "spans[0]")
}

func spanCount(t *testing.T, raw json.RawMessage) int {
	t.Helper()
	var traces map[string]any
	if err := json.Unmarshal(raw, &traces); err != nil {
		t.Fatal(err)
	}
	resourceSpans := asSlice(t, traces["resourceSpans"], "resourceSpans")
	if len(resourceSpans) == 0 {
		return 0
	}
	scopeSpans := asSlice(t, asMap(t, resourceSpans[0], "resourceSpans[0]")["scopeSpans"], "scopeSpans")
	if len(scopeSpans) == 0 {
		return 0
	}
	spansRaw := asMap(t, scopeSpans[0], "scopeSpans[0]")["spans"]
	if spansRaw == nil {
		return 0
	}
	return len(asSlice(t, spansRaw, "spans"))
}

func attrsByKey(span map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, raw := range asSlice(nil, span["attributes"], "attributes") {
		attr := raw.(map[string]any)
		key, _ := attr["key"].(string)
		value, _ := attr["value"].(map[string]any)
		out[key] = value
	}
	return out
}

func assertStringAttr(t *testing.T, attrs map[string]map[string]any, key, want string) {
	t.Helper()
	got, _ := attrs[key]["stringValue"].(string)
	if got != want {
		t.Fatalf("%s = %v, want %q", key, got, want)
	}
}

func assertIntAttr(t *testing.T, attrs map[string]map[string]any, key, want string) {
	t.Helper()
	got := fmt.Sprint(attrs[key]["intValue"])
	if got != want {
		t.Fatalf("%s = %v, want %q", key, got, want)
	}
}

func assertStringArrayAttr(t *testing.T, attrs map[string]map[string]any, key string, want []string) {
	t.Helper()
	array, _ := attrs[key]["arrayValue"].(map[string]any)
	if array == nil {
		t.Fatalf("%s missing array value", key)
	}
	var got []string
	for _, raw := range asSlice(t, array["values"], key+".values") {
		value := asMap(t, raw, key+".value")
		if s, ok := value["stringValue"].(string); ok {
			got = append(got, s)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s = %v, want %v", key, got, want)
	}
}

func asSlice(t *testing.T, v any, name string) []any {
	if out, ok := v.([]any); ok {
		return out
	}
	if t != nil {
		t.Fatalf("%s is not a slice: %T", name, v)
	}
	return nil
}

func asMap(t *testing.T, v any, name string) map[string]any {
	if out, ok := v.(map[string]any); ok {
		return out
	}
	if t != nil {
		t.Fatalf("%s is not a map: %T", name, v)
	}
	return nil
}

func TestGzipSSEIsDecompressedForParsing(t *testing.T) {
	stream := []byte("data: {\"id\":\"chatcmpl_1\",\"model\":\"gpt-test\",\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"lookup\",\"arguments\":\"{}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n" +
		"data: [DONE]\n\n")
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	if _, err := zw.Write(stream); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(gz.Bytes())
	}))
	defer upstream.Close()

	cfg := lab.Config{ListenAddr: ":0", ControlAddr: "127.0.0.1:0"}
	if err := cfg.AddUpstream("default=" + upstream.URL); err != nil {
		t.Fatal(err)
	}
	server, err := lab.NewServer(cfg, lab.NewMemoryStore(), lab.TolerantParser{})
	if err != nil {
		t.Fatal(err)
	}
	proxy := httptest.NewServer(server.ProxyHandler())
	defer proxy.Close()
	control := httptest.NewServer(server.ControlHandler())
	defer control.Close()
	client, err := tracetest.NewClient(control.URL)
	if err != nil {
		t.Fatal(err)
	}
	sessionID := client.NewSession()
	req, err := http.NewRequest(http.MethodPost, proxy.URL+"/v1/chat/completions", bytes.NewReader([]byte(`{"model":"gpt-test","stream":true}`)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(tracetest.SessionHeaderName, sessionID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	calls, err := client.Wait(context.Background(), sessionID, time.Millisecond, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	assertToolCalled(t, calls, "lookup", map[string]any{})
	if calls[0].Normalized.StreamTruncated {
		t.Fatalf("gzip SSE marked truncated")
	}
}

func TestControlNonLoopbackRequiresToken(t *testing.T) {
	cfg := lab.Config{ListenAddr: ":0", ControlAddr: "0.0.0.0:7778"}
	if err := cfg.AddUpstream("default=http://example.test"); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected non-loopback control bind without token to fail")
	}
	cfg.ControlToken = "secret"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("token should allow non-loopback control bind: %v", err)
	}
}

func TestControlClientSendsBearerToken(t *testing.T) {
	cfg := lab.Config{ListenAddr: ":0", ControlAddr: "127.0.0.1:0", ControlToken: "secret"}
	if err := cfg.AddUpstream("default=http://example.test"); err != nil {
		t.Fatal(err)
	}
	server, err := lab.NewServer(cfg, lab.NewMemoryStore(), lab.TolerantParser{})
	if err != nil {
		t.Fatal(err)
	}
	control := httptest.NewServer(server.ControlHandler())
	defer control.Close()
	unauthorized, err := tracetest.NewClient(control.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := unauthorized.Calls(context.Background(), "s"); err == nil {
		t.Fatalf("expected missing token to fail")
	}
	authorized, err := tracetest.NewClient(control.URL, tracetest.WithControlToken("secret"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := authorized.Calls(context.Background(), "s"); err != nil {
		t.Fatalf("authorized client failed: %v", err)
	}
}
