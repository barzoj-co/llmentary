// SPDX-License-Identifier: AGPL-3.0-only

package proxy_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/barzoj-co/llm-test-lab/internal/lab"
	"github.com/barzoj-co/llm-test-lab/internal/openaiapi"
	"github.com/barzoj-co/llm-test-lab/pkg/llmtestlab"
)

type capturedUpstream struct {
	method  string
	path    string
	query   string
	headers http.Header
	body    []byte
}

type proxyRoundtrip struct {
	status     int
	headers    http.Header
	body       []byte
	upstream   capturedUpstream
	call       llmtestlab.Call
	session    llmtestlab.Session
	lab        *llmtestlab.Lab
	proxyURL   string
	controlURL string
}

func startAcceptance(t *testing.T, upstream http.HandlerFunc) (string, string, *capturedUpstream) {
	t.Helper()
	got := &capturedUpstream{}
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.method = r.Method
		got.path = r.URL.Path
		got.query = r.URL.RawQuery
		got.headers = r.Header.Clone()
		body, _ := io.ReadAll(r.Body)
		got.body = body
		upstream(w, r)
	}))
	t.Cleanup(fake.Close)
	cfg := lab.Config{ListenAddr: ":0", ControlAddr: "127.0.0.1:0"}
	if err := cfg.AddUpstream("default=" + fake.URL); err != nil {
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
	return proxy.URL, control.URL, got
}

func doProxy(t *testing.T, method, proxyURL, controlURL, path string, headers map[string]string, body []byte) proxyRoundtrip {
	t.Helper()
	testLab := llmtestlab.New(t, llmtestlab.WithProxyURL(proxyURL), llmtestlab.WithControlURL(controlURL), llmtestlab.WithWait(time.Millisecond, 2*time.Second))
	req, err := http.NewRequest(method, proxyURL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	key, value := testLab.Header()
	req.Header.Set(key, value)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client := &http.Client{Transport: &http.Transport{DisableCompression: true}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	assert := testLab.Expect(ctx)
	calls := assert.Trace().Calls
	if len(calls) == 0 {
		t.Fatal("no recorded calls")
	}
	return proxyRoundtrip{
		status:     resp.StatusCode,
		headers:    resp.Header.Clone(),
		body:       got,
		call:       calls[len(calls)-1],
		session:    llmtestlab.Session{ID: testLab.SessionID(), Calls: calls},
		lab:        testLab,
		proxyURL:   proxyURL,
		controlURL: controlURL,
	}
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func writeJSON(w http.ResponseWriter, status int, body string, extra map[string]string) {
	w.Header().Set("Content-Type", "application/json")
	for k, v := range extra {
		w.Header().Set(k, v)
	}
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

func writeSSE(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, body)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func TestB1ChatNonStreamText(t *testing.T) {
	req := []byte(`{"model":"gpt-fixture","messages":[{"role":"user","content":"hi"}]}`)
	upstreamBody := `{"id":"chatcmpl_b1","model":"gpt-fixture-returned","choices":[{"index":0,"message":{"role":"assistant","content":"hello world"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`
	var cap capturedUpstream
	proxy, control, got := startAcceptance(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, upstreamBody, nil)
	})
	rt := doProxy(t, http.MethodPost, proxy, control, "/v1/chat/completions", nil, req)
	cap = *got
	if rt.call.Normalized.OutputText != "hello world" {
		t.Fatalf("output_text=%q", rt.call.Normalized.OutputText)
	}
	if rt.call.Normalized.Model.Requested != "gpt-fixture" || rt.call.Normalized.Model.Returned != "gpt-fixture-returned" {
		t.Fatalf("model=%+v", rt.call.Normalized.Model)
	}
	if rt.call.Normalized.FinishReason != "stop" {
		t.Fatalf("finish=%q", rt.call.Normalized.FinishReason)
	}
	if rt.call.Normalized.Usage == nil || deref(rt.call.Normalized.Usage.InputTokens) != 3 || deref(rt.call.Normalized.Usage.OutputTokens) != 2 || deref(rt.call.Normalized.Usage.TotalTokens) != 5 {
		t.Fatalf("usage=%+v", rt.call.Normalized.Usage)
	}
	assertByteEqual(t, cap.body, req)
	assertByteEqual(t, rt.body, []byte(upstreamBody))
	assertByteEqual(t, rt.call.RawRequest, req)
	assertByteEqual(t, rt.call.RawResponse, []byte(upstreamBody))
}

func TestB2ChatNonStreamSingleToolCall(t *testing.T) {
	req := []byte(`{"model":"gpt-fixture","messages":[{"role":"user","content":"weather"}]}`)
	upstreamBody := `{"id":"c","model":"gpt-fixture","choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"city\":\"Paris\"}"}}]},"finish_reason":"tool_calls"}]}`
	proxy, control, _ := startAcceptance(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, upstreamBody, nil)
	})
	rt := doProxy(t, http.MethodPost, proxy, control, "/chat/completions", nil, req)
	if len(rt.call.Normalized.ToolCalls) != 1 {
		t.Fatalf("tool calls=%d", len(rt.call.Normalized.ToolCalls))
	}
	tc := rt.call.Normalized.ToolCalls[0]
	if tc.ID != "call_1" || tc.Name != "lookup" || tc.ArgumentsRaw != `{"city":"Paris"}` || string(tc.Arguments) != `{"city":"Paris"}` {
		t.Fatalf("tool=%+v args=%s", tc, string(tc.Arguments))
	}
	if rt.call.Normalized.FinishReason != "tool_calls" {
		t.Fatalf("finish=%q", rt.call.Normalized.FinishReason)
	}
}

func TestB3ChatParallelToolCalls(t *testing.T) {
	req := []byte(`{"model":"gpt-fixture","parallel_tool_calls":false,"messages":[{"role":"user","content":"x"}]}`)
	upstreamBody := `{"id":"c","model":"gpt-fixture","choices":[{"message":{"role":"assistant","tool_calls":[
		{"id":"call_a","type":"function","function":{"name":"one","arguments":"{\"n\":1}"}},
		{"id":"call_b","type":"function","function":{"name":"two","arguments":"{\"n\":2}"}},
		{"id":"call_c","type":"function","function":{"name":"three","arguments":"{\"n\":3}"}}
	]},"finish_reason":"tool_calls"}]}`
	var cap capturedUpstream
	proxy, control, got := startAcceptance(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, upstreamBody, nil)
	})
	rt := doProxy(t, http.MethodPost, proxy, control, "/v1/chat/completions", nil, req)
	cap = *got
	if !bytes.Contains(cap.body, []byte(`"parallel_tool_calls":false`)) {
		t.Fatalf("request not forwarded unchanged: %s", cap.body)
	}
	if len(rt.call.Normalized.ToolCalls) != 3 {
		t.Fatalf("tool calls=%d", len(rt.call.Normalized.ToolCalls))
	}
	if rt.call.Normalized.ToolCalls[0].Name != "one" || rt.call.Normalized.ToolCalls[1].Name != "two" || rt.call.Normalized.ToolCalls[2].Name != "three" {
		t.Fatalf("order=%v", names(rt.call.Normalized.ToolCalls))
	}
	assertByteEqual(t, cap.body, req)
}

func TestB4ChatStreamTextAndKeepAlive(t *testing.T) {
	req := []byte(`{"model":"gpt-fixture","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	stream := ": keep-alive\r\n\r\n" +
		"data: {\"id\":\"c\",\"model\":\"gpt-fixture\",\"choices\":[{\"delta\":{\"content\":\"hel\"}}]}\r\n\r\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"lo\"},\"finish_reason\":\"stop\"}]}\r\n\r\n" +
		"data: [DONE]\r\n\r\n"
	proxy, control, got := startAcceptance(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		_, _ = io.WriteString(w, stream)
	})
	rt := doProxy(t, http.MethodPost, proxy, control, "/v1/chat/completions", nil, req)
	if rt.call.Normalized.OutputText != "hello" {
		t.Fatalf("output_text=%q", rt.call.Normalized.OutputText)
	}
	if rt.call.Normalized.StreamTruncated {
		t.Fatal("stream_truncated")
	}
	assertByteEqual(t, rt.body, []byte(stream))
	assertByteEqual(t, got.body, req)
}

func TestB5ChatStreamSplitToolArgs(t *testing.T) {
	req := []byte(`{"model":"gpt-fixture","stream":true}`)
	stream := "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"lookup\",\"arguments\":\"{\\\"city\\\"\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\":\\\"Pari\\\\u00e9\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n" +
		"data: [DONE]\n\n"
	proxy, control, _ := startAcceptance(t, func(w http.ResponseWriter, _ *http.Request) { writeSSE(w, stream) })
	rt := doProxy(t, http.MethodPost, proxy, control, "/v1/chat/completions", nil, req)
	if len(rt.call.Normalized.ToolCalls) != 1 {
		t.Fatalf("calls=%d", len(rt.call.Normalized.ToolCalls))
	}
	tc := rt.call.Normalized.ToolCalls[0]
	if tc.ID != "call_1" || tc.Name != "lookup" || string(tc.Arguments) != `{"city":"Parié"}` {
		t.Fatalf("tool=%+v args=%s", tc, string(tc.Arguments))
	}
}

func TestB6ChatStreamParallelInterleaved(t *testing.T) {
	req := []byte(`{"model":"gpt-fixture","stream":true}`)
	stream := "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"a\",\"type\":\"function\",\"function\":{\"name\":\"one\",\"arguments\":\"{\\\"n\\\"\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":1,\"id\":\"b\",\"type\":\"function\",\"function\":{\"name\":\"two\",\"arguments\":\"{\\\"n\\\":2}\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\":1}\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":2,\"id\":\"c\",\"type\":\"function\",\"function\":{\"name\":\"three\",\"arguments\":\"{\\\"n\\\":3}\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":1,\"function\":{\"arguments\":\"\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n" +
		"data: [DONE]\n\n"
	proxy, control, _ := startAcceptance(t, func(w http.ResponseWriter, _ *http.Request) { writeSSE(w, stream) })
	rt := doProxy(t, http.MethodPost, proxy, control, "/v1/chat/completions", nil, req)
	if len(rt.call.Normalized.ToolCalls) != 3 {
		t.Fatalf("calls=%d", len(rt.call.Normalized.ToolCalls))
	}
	if rt.call.Normalized.ToolCalls[0].Name != "one" || string(rt.call.Normalized.ToolCalls[0].Arguments) != `{"n":1}` {
		t.Fatalf("0=%+v", rt.call.Normalized.ToolCalls[0])
	}
	if rt.call.Normalized.ToolCalls[1].Name != "two" || rt.call.Normalized.ToolCalls[2].Name != "three" {
		t.Fatalf("names=%v", names(rt.call.Normalized.ToolCalls))
	}
}

func TestB7ChatStreamInvalidToolArgs(t *testing.T) {
	req := []byte(`{"model":"gpt-fixture","stream":true}`)
	stream := "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"c\",\"type\":\"function\",\"function\":{\"name\":\"bad\",\"arguments\":\"not-json\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n" +
		"data: [DONE]\n\n"
	proxy, control, _ := startAcceptance(t, func(w http.ResponseWriter, _ *http.Request) { writeSSE(w, stream) })
	rt := doProxy(t, http.MethodPost, proxy, control, "/v1/chat/completions", nil, req)
	if len(rt.call.Normalized.ToolCalls) != 1 || rt.call.Normalized.ToolCalls[0].ArgsParseError == "" {
		t.Fatalf("expected args_parse_error: %+v", rt.call.Normalized.ToolCalls)
	}
	if !containsWarning(rt.call.Normalized.ParserWarnings, "not valid JSON") {
		t.Fatalf("warnings=%v", rt.call.Normalized.ParserWarnings)
	}
	assertByteEqual(t, rt.body, []byte(stream))
}

func TestB8ChatStreamIncludeUsage(t *testing.T) {
	req := []byte(`{"model":"gpt-fixture","stream":true,"stream_options":{"include_usage":true}}`)
	stream := "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":4,\"completion_tokens\":1,\"total_tokens\":5}}\n\n" +
		"data: [DONE]\n\n"
	proxy, control, _ := startAcceptance(t, func(w http.ResponseWriter, _ *http.Request) { writeSSE(w, stream) })
	rt := doProxy(t, http.MethodPost, proxy, control, "/v1/chat/completions", nil, req)
	if rt.call.Normalized.Usage == nil || rt.call.Normalized.Usage.Source != "final_chunk" {
		t.Fatalf("usage=%+v", rt.call.Normalized.Usage)
	}
}

func TestB9ChatStructuredOutput(t *testing.T) {
	req := []byte(`{"model":"gpt-fixture","response_format":{"type":"json_schema","json_schema":{"name":"ans","strict":true,"schema":{"type":"object","properties":{"answer":{"type":"string"},"count":{"type":"integer"}},"required":["answer","count"],"additionalProperties":false}}},"messages":[{"role":"user","content":"x"}]}`)
	upstreamBody := `{"id":"c","model":"gpt-fixture","choices":[{"message":{"role":"assistant","content":"{\"answer\":\"yes\",\"count\":2}"},"finish_reason":"stop"}]}`
	var cap capturedUpstream
	proxy, control, got := startAcceptance(t, func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, upstreamBody, nil) })
	rt := doProxy(t, http.MethodPost, proxy, control, "/v1/chat/completions", nil, req)
	cap = *got
	assertByteEqual(t, cap.body, req)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(rt.call.Normalized.OutputText), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["answer"] != "yes" || parsed["count"] != float64(2) {
		t.Fatalf("json=%v", parsed)
	}
}

func TestB10ChatRefusal(t *testing.T) {
	t.Run("nonstream", func(t *testing.T) {
		req := []byte(`{"model":"gpt-fixture","messages":[{"role":"user","content":"bad"}]}`)
		upstreamBody := `{"id":"c","model":"gpt-fixture","choices":[{"message":{"role":"assistant","content":null,"refusal":"I cannot help with that"},"finish_reason":"stop"}]}`
		proxy, control, _ := startAcceptance(t, func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, upstreamBody, nil) })
		rt := doProxy(t, http.MethodPost, proxy, control, "/v1/chat/completions", nil, req)
		if rt.call.Normalized.Refusal != "I cannot help with that" || rt.call.Normalized.OutputText != "" {
			t.Fatalf("refusal=%q text=%q finish=%q", rt.call.Normalized.Refusal, rt.call.Normalized.OutputText, rt.call.Normalized.FinishReason)
		}
		if rt.call.Normalized.FinishReason != "stop" {
			t.Fatalf("finish lost: %q", rt.call.Normalized.FinishReason)
		}
	})
	t.Run("stream", func(t *testing.T) {
		req := []byte(`{"model":"gpt-fixture","stream":true}`)
		stream := "data: {\"choices\":[{\"delta\":{\"refusal\":\"I cannot\"}}]}\n\n" +
			"data: {\"choices\":[{\"delta\":{\"refusal\":\" help\"},\"finish_reason\":\"stop\"}]}\n\n" +
			"data: [DONE]\n\n"
		proxy, control, _ := startAcceptance(t, func(w http.ResponseWriter, _ *http.Request) { writeSSE(w, stream) })
		rt := doProxy(t, http.MethodPost, proxy, control, "/v1/chat/completions", nil, req)
		if rt.call.Normalized.Refusal != "I cannot help" || rt.call.Normalized.OutputText != "" {
			t.Fatalf("refusal=%q text=%q", rt.call.Normalized.Refusal, rt.call.Normalized.OutputText)
		}
	})
}

func TestB11ChatUsageDetailsNilVsZero(t *testing.T) {
	req := []byte(`{"model":"gpt-fixture","messages":[{"role":"user","content":"x"}]}`)
	upstreamBody := `{"id":"c","model":"gpt-fixture","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":4,"total_tokens":14,"prompt_tokens_details":{"cached_tokens":2,"audio_tokens":1},"completion_tokens_details":{"reasoning_tokens":3,"accepted_prediction_tokens":0,"rejected_prediction_tokens":5}}}`
	proxy, control, _ := startAcceptance(t, func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, upstreamBody, nil) })
	rt := doProxy(t, http.MethodPost, proxy, control, "/v1/chat/completions", nil, req)
	u := rt.call.Normalized.Usage
	if u == nil || deref(u.CachedInputTokens) != 2 || deref(u.AudioTokens) != 1 || deref(u.ReasoningTokens) != 3 || deref(u.AcceptedPredictionTokens) != 0 || deref(u.RejectedPredictionTokens) != 5 {
		t.Fatalf("usage=%+v", u)
	}
	missing := `{"id":"c","model":"gpt-fixture","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`
	proxy2, control2, _ := startAcceptance(t, func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, missing, nil) })
	rt2 := doProxy(t, http.MethodPost, proxy2, control2, "/v1/chat/completions", nil, req)
	if rt2.call.Normalized.Usage.CachedInputTokens != nil || rt2.call.Normalized.Usage.AudioTokens != nil || rt2.call.Normalized.Usage.AcceptedPredictionTokens != nil {
		t.Fatalf("missing keys should be nil: %+v", rt2.call.Normalized.Usage)
	}
}

func TestB12ChatStreamTruncated(t *testing.T) {
	req := []byte(`{"model":"gpt-fixture","stream":true}`)
	stream := "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n"
	proxy, control, _ := startAcceptance(t, func(w http.ResponseWriter, _ *http.Request) { writeSSE(w, stream) })
	rt := doProxy(t, http.MethodPost, proxy, control, "/v1/chat/completions", nil, req)
	if !rt.call.Normalized.StreamTruncated || rt.call.Normalized.OutputText != "partial" {
		t.Fatalf("truncated=%v text=%q", rt.call.Normalized.StreamTruncated, rt.call.Normalized.OutputText)
	}
}

func TestC1ResponsesNonStreamText(t *testing.T) {
	req := []byte(`{"model":"gpt-fixture","input":"hi"}`)
	upstreamBody := `{"id":"resp_1","model":"gpt-fixture","status":"completed","output":[{"id":"msg","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hello from responses","annotations":[],"logprobs":[]}]}]}`
	proxy, control, _ := startAcceptance(t, func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, upstreamBody, nil) })
	rt := doProxy(t, http.MethodPost, proxy, control, "/v1/responses", nil, req)
	if rt.call.Normalized.OutputText != "hello from responses" || rt.call.Normalized.FinishReason != "stop" {
		t.Fatalf("text=%q finish=%q", rt.call.Normalized.OutputText, rt.call.Normalized.FinishReason)
	}
}

func TestC2ResponsesFunctionCalls(t *testing.T) {
	req := []byte(`{"model":"gpt-fixture","input":"tools"}`)
	upstreamBody := `{"id":"resp_1","model":"gpt-fixture","status":"completed","output":[
		{"id":"fc_0","type":"function_call","call_id":"call_a","name":"one","arguments":"{\"n\":1}"},
		{"id":"fc_1","type":"function_call","call_id":"call_b","name":"two","arguments":"{\"n\":2}"}
	]}`
	proxy, control, _ := startAcceptance(t, func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, upstreamBody, nil) })
	rt := doProxy(t, http.MethodPost, proxy, control, "/v1/responses", nil, req)
	if len(rt.call.Normalized.ToolCalls) != 2 {
		t.Fatalf("calls=%d", len(rt.call.Normalized.ToolCalls))
	}
	if rt.call.Normalized.ToolCalls[0].ID != "call_a" || rt.call.Normalized.ToolCalls[0].ItemID != "fc_0" || rt.call.Normalized.ToolCalls[1].Name != "two" {
		t.Fatalf("tools=%+v", rt.call.Normalized.ToolCalls)
	}
}

func TestC3ResponsesCustomToolCall(t *testing.T) {
	req := []byte(`{"model":"gpt-fixture","input":"x"}`)
	upstreamBody := `{"id":"resp_1","status":"completed","output":[{"id":"ctc","type":"custom_tool_call","call_id":"call_custom","name":"browser","input":"open https://example.test"}]}`
	proxy, control, _ := startAcceptance(t, func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, upstreamBody, nil) })
	rt := doProxy(t, http.MethodPost, proxy, control, "/v1/responses", nil, req)
	if len(rt.call.Normalized.ToolCalls) != 1 || rt.call.Normalized.ToolCalls[0].Kind != "custom" {
		t.Fatalf("tools=%+v", rt.call.Normalized.ToolCalls)
	}
}

func TestC4ResponsesStreamDiscriminator(t *testing.T) {
	req := []byte(`{"model":"gpt-fixture","stream":true,"input":"hi"}`)
	delta := `{"type":"response.output_text.delta","delta":"hello","content_index":0,"item_id":"m","output_index":0,"sequence_number":1,"logprobs":[]}`
	completed := `{"type":"response.completed","sequence_number":2,"response":{"id":"resp_1","created_at":1,"model":"gpt-fixture","object":"response","output":[{"id":"m","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hello","annotations":[],"logprobs":[]}]}],"parallel_tool_calls":true,"status":"completed","tool_choice":"auto","tools":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2,"input_tokens_details":{"cached_tokens":0,"cache_write_tokens":0},"output_tokens_details":{"reasoning_tokens":0}}}}`
	stream := "event: response.created\ndata: {\"type\":\"response.created\",\"sequence_number\":0,\"response\":{\"id\":\"resp_1\",\"created_at\":1,\"model\":\"gpt-fixture\",\"object\":\"response\",\"output\":[],\"parallel_tool_calls\":true,\"status\":\"in_progress\",\"tool_choice\":\"auto\",\"tools\":[]}}\n\n" +
		"event: response.output_text.delta\ndata: " + delta + "\n\n" +
		"event: response.completed\ndata: " + completed + "\n\n"
	proxy, control, _ := startAcceptance(t, func(w http.ResponseWriter, _ *http.Request) { writeSSE(w, stream) })
	rt := doProxy(t, http.MethodPost, proxy, control, "/v1/responses", nil, req)
	if rt.call.Normalized.OutputText != "hello" || rt.call.Normalized.FinishReason != "stop" {
		t.Fatalf("text=%q finish=%q", rt.call.Normalized.OutputText, rt.call.Normalized.FinishReason)
	}
	var ev openaiapi.ResponseStreamEvent
	if err := json.Unmarshal([]byte(delta), &ev); err != nil {
		t.Fatal(err)
	}
	typed, err := ev.ValueByDiscriminator()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := typed.(openaiapi.ResponseTextDeltaEvent); !ok {
		t.Fatalf("discriminator returned %T", typed)
	}
	name, err := ev.Discriminator()
	if err != nil || name != "response.output_text.delta" {
		t.Fatalf("discriminator=%q err=%v", name, err)
	}
}

func TestC5ResponsesStreamFunctionCall(t *testing.T) {
	req := []byte(`{"model":"gpt-fixture","stream":true}`)
	stream := "event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"id\":\"fc_1\",\"type\":\"function_call\",\"call_id\":\"call_1\",\"name\":\"lookup\",\"arguments\":\"\"}}\n\n" +
		"event: response.function_call_arguments.delta\ndata: {\"type\":\"response.function_call_arguments.delta\",\"item_id\":\"fc_1\",\"output_index\":0,\"delta\":\"{\\\"city\\\"\",\"sequence_number\":1}\n\n" +
		"event: response.function_call_arguments.delta\ndata: {\"type\":\"response.function_call_arguments.delta\",\"item_id\":\"fc_1\",\"output_index\":0,\"delta\":\":\\\"Paris\\\"}\",\"sequence_number\":2}\n\n" +
		"event: response.function_call_arguments.done\ndata: {\"type\":\"response.function_call_arguments.done\",\"item_id\":\"fc_1\",\"output_index\":0,\"arguments\":\"{\\\"city\\\":\\\"Paris\\\"}\",\"sequence_number\":3}\n\n" +
		"event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"id\":\"fc_1\",\"type\":\"function_call\",\"call_id\":\"call_1\",\"name\":\"lookup\",\"arguments\":\"{\\\"city\\\":\\\"Paris\\\"}\"},\"sequence_number\":4}\n\n" +
		"event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"output_index\":1,\"item\":{\"id\":\"fc_2\",\"type\":\"function_call\",\"call_id\":\"call_2\",\"name\":\"other\",\"arguments\":\"{\\\"n\\\":1}\"},\"sequence_number\":5}\n\n" +
		"event: response.completed\ndata: {\"type\":\"response.completed\",\"sequence_number\":6,\"response\":{\"id\":\"resp_1\",\"created_at\":1,\"model\":\"gpt-fixture\",\"object\":\"response\",\"status\":\"completed\",\"output\":[{\"id\":\"fc_1\",\"type\":\"function_call\",\"call_id\":\"call_1\",\"name\":\"lookup\",\"arguments\":\"{\\\"city\\\":\\\"Paris\\\"}\"},{\"id\":\"fc_2\",\"type\":\"function_call\",\"call_id\":\"call_2\",\"name\":\"other\",\"arguments\":\"{\\\"n\\\":1}\"}],\"parallel_tool_calls\":true,\"tool_choice\":\"auto\",\"tools\":[]}}\n\n"
	proxy, control, _ := startAcceptance(t, func(w http.ResponseWriter, _ *http.Request) { writeSSE(w, stream) })
	rt := doProxy(t, http.MethodPost, proxy, control, "/v1/responses", nil, req)
	if len(rt.call.Normalized.ToolCalls) != 2 {
		t.Fatalf("calls=%+v", rt.call.Normalized.ToolCalls)
	}
	if rt.call.Normalized.ToolCalls[0].ArgumentsRaw != `{"city":"Paris"}` || rt.call.Normalized.ToolCalls[1].Name != "other" {
		t.Fatalf("tools=%+v", rt.call.Normalized.ToolCalls)
	}
}

func TestC6ResponsesIncompleteAndFailed(t *testing.T) {
	req := []byte(`{"model":"gpt-fixture","input":"x"}`)
	t.Run("max_output_tokens", func(t *testing.T) {
		body := `{"id":"r","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[{"id":"m","type":"message","role":"assistant","status":"incomplete","content":[{"type":"output_text","text":"cut","annotations":[],"logprobs":[]}]}]}`
		proxy, control, _ := startAcceptance(t, func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, body, nil) })
		rt := doProxy(t, http.MethodPost, proxy, control, "/v1/responses", nil, req)
		if rt.call.Normalized.FinishReason != "length" {
			t.Fatalf("finish=%q", rt.call.Normalized.FinishReason)
		}
	})
	t.Run("content_filter", func(t *testing.T) {
		body := `{"id":"r","status":"incomplete","incomplete_details":{"reason":"content_filter"},"output":[]}`
		proxy, control, _ := startAcceptance(t, func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, body, nil) })
		rt := doProxy(t, http.MethodPost, proxy, control, "/v1/responses", nil, req)
		if rt.call.Normalized.FinishReason != "content_filter" {
			t.Fatalf("finish=%q", rt.call.Normalized.FinishReason)
		}
	})
	t.Run("failed", func(t *testing.T) {
		body := `{"id":"r","status":"failed","error":{"code":"server_error","message":"boom"},"output":[]}`
		proxy, control, _ := startAcceptance(t, func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, body, nil) })
		rt := doProxy(t, http.MethodPost, proxy, control, "/v1/responses", nil, req)
		if rt.call.Normalized.FinishReason != "error" || rt.call.Normalized.Error == nil {
			t.Fatalf("finish=%q err=%+v", rt.call.Normalized.FinishReason, rt.call.Normalized.Error)
		}
	})
}

func TestC7ResponsesStructuredOutput(t *testing.T) {
	req := []byte(`{"model":"gpt-fixture","input":"x","text":{"format":{"type":"json_schema","name":"ans","schema":{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"]}}}}`)
	body := `{"id":"r","status":"completed","output":[{"id":"m","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"{\"answer\":\"ok\"}","annotations":[],"logprobs":[]}]}]}`
	var cap capturedUpstream
	proxy, control, got := startAcceptance(t, func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, body, nil) })
	rt := doProxy(t, http.MethodPost, proxy, control, "/v1/responses", nil, req)
	cap = *got
	assertByteEqual(t, cap.body, req)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(rt.call.Normalized.OutputText), &parsed); err != nil || parsed["answer"] != "ok" {
		t.Fatalf("output=%q", rt.call.Normalized.OutputText)
	}
}

func TestC8ResponsesRefusal(t *testing.T) {
	req := []byte(`{"model":"gpt-fixture","input":"bad"}`)
	t.Run("nonstream", func(t *testing.T) {
		body := `{"id":"r","status":"completed","output":[{"id":"m","type":"message","role":"assistant","status":"completed","content":[{"type":"refusal","refusal":"nope"}]}]}`
		proxy, control, _ := startAcceptance(t, func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, body, nil) })
		rt := doProxy(t, http.MethodPost, proxy, control, "/v1/responses", nil, req)
		if rt.call.Normalized.Refusal != "nope" {
			t.Fatalf("refusal=%q", rt.call.Normalized.Refusal)
		}
	})
	t.Run("stream", func(t *testing.T) {
		stream := "event: response.refusal.delta\ndata: {\"type\":\"response.refusal.delta\",\"delta\":\"no\",\"content_index\":0,\"item_id\":\"m\",\"output_index\":0,\"sequence_number\":1}\n\n" +
			"event: response.refusal.done\ndata: {\"type\":\"response.refusal.done\",\"refusal\":\"nope\",\"content_index\":0,\"item_id\":\"m\",\"output_index\":0,\"sequence_number\":2}\n\n" +
			"event: response.completed\ndata: {\"type\":\"response.completed\",\"sequence_number\":3,\"response\":{\"id\":\"r\",\"created_at\":1,\"model\":\"gpt-fixture\",\"object\":\"response\",\"status\":\"completed\",\"output\":[{\"id\":\"m\",\"type\":\"message\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"refusal\",\"refusal\":\"nope\"}]}],\"parallel_tool_calls\":true,\"tool_choice\":\"auto\",\"tools\":[]}}\n\n"
		proxy, control, _ := startAcceptance(t, func(w http.ResponseWriter, _ *http.Request) { writeSSE(w, stream) })
		rt := doProxy(t, http.MethodPost, proxy, control, "/v1/responses", nil, []byte(`{"model":"gpt-fixture","stream":true}`))
		if rt.call.Normalized.Refusal != "nope" {
			t.Fatalf("refusal=%q", rt.call.Normalized.Refusal)
		}
	})
}

func TestC9ResponsesUsageAndReasoning(t *testing.T) {
	req := []byte(`{"model":"gpt-fixture","input":"think"}`)
	body := `{"id":"r","status":"completed","usage":{"input_tokens":8,"output_tokens":4,"total_tokens":12,"input_tokens_details":{"cached_tokens":3,"cache_write_tokens":0},"output_tokens_details":{"reasoning_tokens":2}},"output":[{"id":"rs","type":"reasoning","summary":[],"encrypted_content":"enc"}]}`
	proxy, control, _ := startAcceptance(t, func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, body, nil) })
	rt := doProxy(t, http.MethodPost, proxy, control, "/v1/responses", nil, req)
	if rt.call.Normalized.Usage == nil || deref(rt.call.Normalized.Usage.CachedInputTokens) != 3 || deref(rt.call.Normalized.Usage.ReasoningTokens) != 2 {
		t.Fatalf("usage=%+v", rt.call.Normalized.Usage)
	}
	if rt.call.Normalized.Reasoning == nil || !rt.call.Normalized.Reasoning.Present || !rt.call.Normalized.Reasoning.Encrypted {
		t.Fatalf("reasoning=%+v", rt.call.Normalized.Reasoning)
	}
}

func TestC10ResponsesPreviousResponseID(t *testing.T) {
	req := []byte(`{"model":"gpt-fixture","previous_response_id":"resp_prev","conversation":"conv_1","input":"hi"}`)
	body := `{"id":"r","status":"completed","output":[]}`
	proxy, control, _ := startAcceptance(t, func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, body, nil) })
	rt := doProxy(t, http.MethodPost, proxy, control, "/v1/responses", nil, req)
	if !rt.call.Normalized.ContextServerSide || rt.call.Normalized.PreviousResponseID != "resp_prev" {
		t.Fatalf("ctx=%v prev=%q", rt.call.Normalized.ContextServerSide, rt.call.Normalized.PreviousResponseID)
	}
}

func TestC11ResponsesUnknownEvent(t *testing.T) {
	req := []byte(`{"model":"gpt-fixture","stream":true}`)
	stream := "event: response.some_future_event\ndata: {\"type\":\"response.some_future_event\",\"hello\":true}\n\n" +
		"event: response.completed\ndata: {\"type\":\"response.completed\",\"sequence_number\":1,\"response\":{\"id\":\"r\",\"created_at\":1,\"model\":\"gpt-fixture\",\"object\":\"response\",\"status\":\"completed\",\"output\":[{\"id\":\"m\",\"type\":\"message\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"ok\",\"annotations\":[],\"logprobs\":[]}]}],\"parallel_tool_calls\":true,\"tool_choice\":\"auto\",\"tools\":[]}}\n\n"
	proxy, control, _ := startAcceptance(t, func(w http.ResponseWriter, _ *http.Request) { writeSSE(w, stream) })
	rt := doProxy(t, http.MethodPost, proxy, control, "/v1/responses", nil, req)
	if !containsWarning(rt.call.Normalized.ParserWarnings, "response.some_future_event") {
		t.Fatalf("warnings=%v", rt.call.Normalized.ParserWarnings)
	}
	if rt.call.Normalized.OutputText != "ok" {
		t.Fatalf("text=%q", rt.call.Normalized.OutputText)
	}
	found := false
	for _, ev := range rt.call.SSEEvents {
		if ev.Event == "response.some_future_event" {
			found = true
		}
	}
	if !found {
		t.Fatalf("sse_events=%+v", rt.call.SSEEvents)
	}
	assertByteEqual(t, rt.body, []byte(stream))
}

func TestD1ErrorStatuses(t *testing.T) {
	bodies := map[string][]byte{
		"/v1/chat/completions": []byte(`{"model":"gpt-fixture","messages":[{"role":"user","content":"x"}]}`),
		"/v1/responses":        []byte(`{"model":"gpt-fixture","input":"x"}`),
	}
	cases := []struct {
		status     int
		body       string
		headers    map[string]string
		normalized string
		code       string
	}{
		{400, `{"error":{"type":"invalid_request_error","code":"bad","message":"nope"}}`, nil, "bad_request", "bad"},
		{401, `{"error":{"type":"auth_error","code":null,"message":"key"}}`, nil, "auth", ""},
		{403, `{"error":{"type":"permission","code":"forbidden","message":"no"}}`, nil, "auth", "forbidden"},
		{404, `{"error":{"type":"not_found","code":"missing","message":"gone"}}`, nil, "bad_request", "missing"},
		{429, `{"error":{"type":"rate_limit_error","code":"rate_limit_exceeded","message":"slow"}}`, map[string]string{"Retry-After": "2"}, "rate_limit", "rate_limit_exceeded"},
		{500, `{"error":{"type":"server_error","code":"internal","message":"boom"}}`, nil, "server", "internal"},
		{503, `{"error":{"type":"server_error","code":"overloaded","message":"later"}}`, nil, "server", "overloaded"},
	}
	for _, tc := range cases {
		for _, path := range []string{"/v1/chat/completions", "/v1/responses"} {
			for _, stream := range []bool{false, true} {
				name := strings.ReplaceAll(http.StatusText(tc.status)+path, " ", "_")
				if stream {
					name += "_stream_request"
				}
				t.Run(name, func(t *testing.T) {
					req := append([]byte(nil), bodies[path]...)
					if stream {
						req = []byte(strings.TrimSuffix(string(req), "}") + `,"stream":true}`)
					}
					var cap capturedUpstream
					proxy, control, got := startAcceptance(t, func(w http.ResponseWriter, _ *http.Request) {
						writeJSON(w, tc.status, tc.body, tc.headers)
					})
					rt := doProxy(t, http.MethodPost, proxy, control, path, nil, req)
					cap = *got
					if rt.status != tc.status {
						t.Fatalf("status=%d", rt.status)
					}
					assertByteEqual(t, rt.body, []byte(tc.body))
					assertByteEqual(t, cap.body, req)
					if rt.call.Normalized.Error == nil || rt.call.Normalized.Error.Normalized != tc.normalized || rt.call.Normalized.Error.Code != tc.code {
						t.Fatalf("error=%+v", rt.call.Normalized.Error)
					}
					if tc.headers["Retry-After"] != "" && rt.call.RetryAfter != "2" {
						t.Fatalf("retry_after=%q", rt.call.RetryAfter)
					}
				})
			}
		}
	}
}

func TestD2OutOfSpecStatuses(t *testing.T) {
	req := []byte(`{"model":"gpt-fixture","messages":[{"role":"user","content":"x"}]}`)
	html := `<html>balancer</html>`
	for _, status := range []int{http.StatusPaymentRequired, http.StatusRequestTimeout, http.StatusBadGateway, http.StatusGatewayTimeout} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			proxy, control, _ := startAcceptance(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				w.WriteHeader(status)
				_, _ = io.WriteString(w, html)
			})
			rt := doProxy(t, http.MethodPost, proxy, control, "/v1/chat/completions", nil, req)
			if rt.status != status || string(rt.body) != html {
				t.Fatalf("status=%d body=%q", rt.status, rt.body)
			}
			if rt.call.Normalized.Error == nil || rt.call.Normalized.Error.HTTPStatus != status {
				t.Fatalf("error=%+v", rt.call.Normalized.Error)
			}
		})
	}
}

func TestD3ChatMidStreamError(t *testing.T) {
	req := []byte(`{"model":"gpt-fixture","stream":true}`)
	stream := "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n" +
		"data: {\"error\":{\"type\":\"too_many_requests\",\"code\":\"no_capacity\",\"message\":\"try later\"}}\n\n"
	proxy, control, _ := startAcceptance(t, func(w http.ResponseWriter, _ *http.Request) { writeSSE(w, stream) })
	rt := doProxy(t, http.MethodPost, proxy, control, "/v1/chat/completions", nil, req)
	if rt.call.Normalized.Error == nil || !rt.call.Normalized.Error.MidStream || rt.call.Normalized.OutputText != "partial" {
		t.Fatalf("err=%+v text=%q", rt.call.Normalized.Error, rt.call.Normalized.OutputText)
	}
}

func TestD4ResponsesStreamErrorShapes(t *testing.T) {
	req := []byte(`{"model":"gpt-fixture","stream":true}`)
	t.Run("spec_toplevel", func(t *testing.T) {
		stream := "event: error\ndata: {\"type\":\"error\",\"code\":\"server_error\",\"message\":\"boom\",\"sequence_number\":1}\n\n"
		proxy, control, _ := startAcceptance(t, func(w http.ResponseWriter, _ *http.Request) { writeSSE(w, stream) })
		rt := doProxy(t, http.MethodPost, proxy, control, "/v1/responses", nil, req)
		if rt.call.Normalized.Error == nil || !rt.call.Normalized.Error.MidStream || rt.call.Normalized.FinishReason != "error" {
			t.Fatalf("err=%+v finish=%q", rt.call.Normalized.Error, rt.call.Normalized.FinishReason)
		}
	})
	t.Run("nested_error", func(t *testing.T) {
		stream := "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"server_error\",\"code\":\"nested\",\"message\":\"nested boom\"}}\n\n"
		proxy, control, _ := startAcceptance(t, func(w http.ResponseWriter, _ *http.Request) { writeSSE(w, stream) })
		rt := doProxy(t, http.MethodPost, proxy, control, "/v1/responses", nil, req)
		if rt.call.Normalized.Error == nil || !rt.call.Normalized.Error.MidStream {
			t.Fatalf("err=%+v", rt.call.Normalized.Error)
		}
	})
}

func TestD5RequestDecodeErrorPassthrough(t *testing.T) {
	upstreamBody := `{"error":{"type":"invalid_request_error","code":"bad","message":"upstream said no"}}`
	t.Run("malformed", func(t *testing.T) {
		req := []byte(`{not-json`)
		var cap capturedUpstream
		proxy, control, got := startAcceptance(t, func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, 400, upstreamBody, nil)
		})
		rt := doProxy(t, http.MethodPost, proxy, control, "/v1/chat/completions", nil, req)
		cap = *got
		if rt.status != 400 {
			t.Fatalf("proxy answered %d", rt.status)
		}
		assertByteEqual(t, cap.body, req)
		assertByteEqual(t, rt.body, []byte(upstreamBody))
		if !containsWarning(rt.call.Normalized.ParserWarnings, "request does not match spec") {
			t.Fatalf("warnings=%v", rt.call.Normalized.ParserWarnings)
		}
	})
	t.Run("wrong_type", func(t *testing.T) {
		req := []byte(`{"model":"gpt-fixture","messages":123}`)
		var cap capturedUpstream
		proxy, control, got := startAcceptance(t, func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, 422, upstreamBody, nil)
		})
		rt := doProxy(t, http.MethodPost, proxy, control, "/v1/chat/completions", nil, req)
		cap = *got
		if rt.status != 422 {
			t.Fatalf("proxy answered %d", rt.status)
		}
		assertByteEqual(t, cap.body, req)
		if !containsWarning(rt.call.Normalized.ParserWarnings, "request does not match spec") {
			t.Fatalf("warnings=%v", rt.call.Normalized.ParserWarnings)
		}
	})
}

func TestD6UpstreamUnreachable(t *testing.T) {
	cfg := lab.Config{ListenAddr: ":0", ControlAddr: "127.0.0.1:0"}
	if err := cfg.AddUpstream("default=http://127.0.0.1:1"); err != nil {
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
	rt := doProxy(t, http.MethodPost, proxy.URL, control.URL, "/v1/chat/completions", nil, []byte(`{"model":"x"}`))
	if rt.status != http.StatusBadGateway {
		t.Fatalf("status=%d", rt.status)
	}
	if rt.call.Normalized.Error == nil || rt.call.Normalized.Error.Normalized != "network" {
		t.Fatalf("error=%+v", rt.call.Normalized.Error)
	}
}

func TestE1AzureClassic(t *testing.T) {
	req := []byte(`{"model":"ignored","messages":[{"role":"user","content":"hi"}]}`)
	var cap capturedUpstream
	proxy, control, got := startAcceptance(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, `{"id":"c","model":"dep","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`, nil)
	})
	rt := doProxy(t, http.MethodPost, proxy, control, "/openai/deployments/my-dep/chat/completions?api-version=2024-10-21", map[string]string{"api-key": "super-secret"}, req)
	cap = *got
	if cap.path != "/openai/deployments/my-dep/chat/completions" || cap.query != "api-version=2024-10-21" {
		t.Fatalf("path=%q query=%q", cap.path, cap.query)
	}
	if cap.headers.Get("api-key") != "super-secret" {
		t.Fatalf("api-key not forwarded")
	}
	if rt.call.Upstream.Deployment != "my-dep" || rt.call.Upstream.APIVersion != "2024-10-21" {
		t.Fatalf("upstream=%+v", rt.call.Upstream)
	}
	auth := rt.call.Request.Headers["Api-Key"]
	if len(auth) == 0 {
		auth = rt.call.Request.Headers["api-key"]
	}
	if len(auth) == 0 || auth[0] == "super-secret" || !strings.HasPrefix(auth[0], "sha256:") {
		t.Fatalf("api-key not hashed: %v", rt.call.Request.Headers)
	}
}

func TestE2AzureV1(t *testing.T) {
	req := []byte(`{"model":"gpt-fixture","messages":[{"role":"user","content":"hi"}]}`)
	proxy, control, got := startAcceptance(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, `{"id":"c","model":"gpt-fixture","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`, nil)
	})
	_ = doProxy(t, http.MethodPost, proxy, control, "/openai/v1/chat/completions", map[string]string{"Authorization": "Bearer azure-token"}, req)
	if got.path != "/openai/v1/chat/completions" || got.headers.Get("Authorization") != "Bearer azure-token" {
		t.Fatalf("path=%q auth=%q", got.path, got.headers.Get("Authorization"))
	}
	_ = doProxy(t, http.MethodPost, proxy, control, "/openai/v1/responses?api-version=2025-01-01", map[string]string{"Authorization": "Bearer azure-token"}, []byte(`{"model":"gpt-fixture","input":"hi"}`))
	if !strings.HasSuffix(got.path, "/responses") {
		t.Fatalf("responses path=%q", got.path)
	}
}

func TestE3AzureContentFilter(t *testing.T) {
	req := []byte(`{"model":"gpt-fixture","stream":true}`)
	t.Run("empty_choices_then_tool", func(t *testing.T) {
		body, err := testdataRead("azure_content_filter_empty_choices.sse")
		if err != nil {
			t.Fatal(err)
		}
		proxy, control, _ := startAcceptance(t, func(w http.ResponseWriter, _ *http.Request) { writeSSE(w, string(body)) })
		rt := doProxy(t, http.MethodPost, proxy, control, "/openai/deployments/dep/chat/completions?api-version=2024-10-21", nil, req)
		if rt.call.Normalized.ContentFilter == nil {
			t.Fatal("content_filter not set")
		}
		if rt.call.Normalized.FinishReason != "tool_calls" {
			t.Fatalf("finish=%q", rt.call.Normalized.FinishReason)
		}
		if len(rt.call.Normalized.ParserWarnings) != 0 {
			t.Fatalf("parser warnings=%v", rt.call.Normalized.ParserWarnings)
		}
	})
	t.Run("finish_content_filter", func(t *testing.T) {
		stream := "data: {\"choices\":[],\"prompt_filter_results\":[{\"prompt_index\":0,\"content_filter_results\":{\"hate\":{\"filtered\":true,\"severity\":\"high\"}}}]}\n\n" +
			"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"content_filter\",\"content_filter_results\":{\"hate\":{\"filtered\":true,\"severity\":\"high\"}}}]}\n\n" +
			"data: [DONE]\n\n"
		proxy, control, _ := startAcceptance(t, func(w http.ResponseWriter, _ *http.Request) { writeSSE(w, stream) })
		rt := doProxy(t, http.MethodPost, proxy, control, "/openai/deployments/dep/chat/completions?api-version=2024-10-21", nil, req)
		if rt.call.Normalized.ContentFilter == nil || !rt.call.Normalized.ContentFilter.PromptFiltered {
			t.Fatalf("content_filter=%+v", rt.call.Normalized.ContentFilter)
		}
		if rt.call.Normalized.FinishReason != "content_filter" {
			t.Fatalf("finish=%q", rt.call.Normalized.FinishReason)
		}
	})
}

func TestE4OpenRouter(t *testing.T) {
	req := []byte(`{"model":"openrouter/auto","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	stream := ": OPENROUTER PROCESSING\n\n" +
		"data: {\"id\":\"c\",\"model\":\"openrouter/auto\",\"provider\":\"Anthropic\",\"choices\":[{\"delta\":{\"content\":\"hi\",\"reasoning\":\"think\"},\"native_finish_reason\":\"end\"}]}\n\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":1,\"total_tokens\":3,\"cost\":0.012}}\n\n" +
		"data: [DONE]\n\n"
	proxy, control, _ := startAcceptance(t, func(w http.ResponseWriter, _ *http.Request) { writeSSE(w, stream) })
	rt := doProxy(t, http.MethodPost, proxy, control, "/api/v1/chat/completions", nil, req)
	if rt.call.Normalized.ServedBy != "Anthropic" {
		t.Fatalf("served_by=%q", rt.call.Normalized.ServedBy)
	}
	if rt.call.Normalized.NativeFinishReason != "end" {
		t.Fatalf("native=%q", rt.call.Normalized.NativeFinishReason)
	}
	if rt.call.Normalized.Usage == nil || rt.call.Normalized.Usage.CostUSD == nil || *rt.call.Normalized.Usage.CostUSD != 0.012 {
		t.Fatalf("cost=%+v", rt.call.Normalized.Usage)
	}
	if rt.call.Normalized.Reasoning == nil || rt.call.Normalized.Reasoning.Chars == 0 {
		t.Fatalf("reasoning=%+v", rt.call.Normalized.Reasoning)
	}
}

func TestE5ForwardCompatUnknownFields(t *testing.T) {
	req := []byte(`{"model":"gpt-fixture","messages":[{"role":"user","content":"hi"}],"future_root":true}`)
	upstreamBody := `{"id":"c","model":"gpt-fixture","future_root":1,"choices":[{"future_choice":true,"message":{"role":"assistant","content":"ok","future_delta":false},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2,"future_usage":true}}`
	var cap capturedUpstream
	proxy, control, got := startAcceptance(t, func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, upstreamBody, nil) })
	rt := doProxy(t, http.MethodPost, proxy, control, "/v1/chat/completions", nil, req)
	cap = *got
	assertByteEqual(t, cap.body, req)
	assertByteEqual(t, rt.body, []byte(upstreamBody))
	if rt.call.Normalized.OutputText != "ok" {
		t.Fatalf("text=%q", rt.call.Normalized.OutputText)
	}
}

func TestE6GzipRawBytes(t *testing.T) {
	plain := []byte(`{"id":"c","model":"gpt-fixture","choices":[{"message":{"role":"assistant","content":"gz"},"finish_reason":"stop"}]}`)
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write(plain)
	_ = zw.Close()
	req := []byte(`{"model":"gpt-fixture","messages":[{"role":"user","content":"x"}]}`)
	var cap capturedUpstream
	proxy, control, got := startAcceptance(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(gz.Bytes())
	})
	rt := doProxy(t, http.MethodPost, proxy, control, "/v1/chat/completions", nil, req)
	cap = *got
	assertByteEqual(t, cap.body, req)
	if sha256Hex(rt.body) != sha256Hex(gz.Bytes()) {
		t.Fatalf("client did not receive gzip bytes")
	}
	assertByteEqual(t, rt.call.RawRequest, req)
	assertByteEqual(t, rt.call.RawResponse, plain)
}

func TestE7PassthroughNonSpecPaths(t *testing.T) {
	req := []byte(`{"input":"x"}`)
	for _, path := range []string{"/v1/models", "/v1/embeddings", "/v1/responses/resp_123", "/foo"} {
		method := http.MethodPost
		if path == "/v1/models" || strings.HasPrefix(path, "/v1/responses/") {
			method = http.MethodGet
		}
		var cap capturedUpstream
		proxy, control, got := startAcceptance(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true}`))
		})
		rt := doProxy(t, method, proxy, control, path, nil, req)
		cap = *got
		if rt.call.APIFamily != "passthrough" {
			t.Fatalf("%s family=%q", path, rt.call.APIFamily)
		}
		if cap.path != path && !strings.HasSuffix(cap.path, path) {
			t.Fatalf("upstream path=%q want %q", cap.path, path)
		}
		assertByteEqual(t, rt.body, []byte(`{"ok":true}`))
	}
}

func TestA7NoHandwrittenChoicesTags(t *testing.T) {
	root := filepath.Join("..", "..", "internal", "lab")
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !bytes.Contains(raw, []byte("`json:\"choices\"`")) && !bytes.Contains(raw, []byte(`json:"choices"`)) {
			return nil
		}
		rel := filepath.Base(path)
		if rel == "extensions.go" {
			return nil
		}
		t.Errorf("%s still has a handwritten json:\"choices\" tag", path)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func deref(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}

func names(calls []llmtestlab.ToolCall) []string {
	out := make([]string, len(calls))
	for i, c := range calls {
		out[i] = c.Name
	}
	return out
}

func containsWarning(warnings []string, needle string) bool {
	for _, w := range warnings {
		if strings.Contains(w, needle) {
			return true
		}
	}
	return false
}

func assertByteEqual(t *testing.T, got, want []byte) {
	t.Helper()
	if !bytes.Equal(got, want) {
		t.Fatalf("bytes mismatch sha got=%s want=%s\n got=%q\nwant=%q", sha256Hex(got), sha256Hex(want), got, want)
	}
}

func testdataRead(name string) ([]byte, error) {
	return os.ReadFile(filepath.Join("testdata", name))
}
