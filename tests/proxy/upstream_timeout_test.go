// SPDX-License-Identifier: AGPL-3.0-only

package proxy_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/barzoj-co/llmentary/internal/proxy"
)

var (
	responsesJSONBody   = []byte(`{"model":"gpt-fixture","input":"hi"}`)
	responsesStreamBody = []byte(`{"model":"gpt-fixture","input":"hi","stream":true}`)
	chatJSONBody        = []byte(`{"model":"gpt-fixture","messages":[{"role":"user","content":"hi"}]}`)
	chatStreamBody      = []byte(`{"model":"gpt-fixture","messages":[{"role":"user","content":"hi"}],"stream":true}`)
)

func stallHeadersForever() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}
}

func stallHeadersThenOK(hold time.Duration) http.HandlerFunc {
	return slowHeaders(hold)
}

func stallAfterFirstResponsesSSE() http.HandlerFunc {
	chunk := "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"chunk-one\"}\n\n"
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		_, _ = io.WriteString(w, chunk)
		if flusher != nil {
			flusher.Flush()
		}
		<-r.Context().Done()
	}
}

func stallAfterFirstChatSSE() http.HandlerFunc {
	chunk := `data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1,"model":"gpt-fixture","choices":[{"index":0,"delta":{"role":"assistant","content":"chunk-one"},"finish_reason":null}]}` + "\n\n"
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		_, _ = io.WriteString(w, chunk)
		if flusher != nil {
			flusher.Flush()
		}
		<-r.Context().Done()
	}
}

func stallAfterJSONPrefix() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		_, _ = io.WriteString(w, `{"id":"partial"`)
		if flusher != nil {
			flusher.Flush()
		}
		<-r.Context().Done()
	}
}

func stallOrOK(stallHeader string) http.HandlerFunc {
	ok := responsesOK()
	stall := stallHeadersForever()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(stallHeader) == "1" {
			stall(w, r)
			return
		}
		ok(w, r)
	}
}

func withHeaderTimeout(d time.Duration) func(*proxy.Config) {
	return func(cfg *proxy.Config) { cfg.UpstreamHeaderTimeout = d }
}

func withIdleTimeout(d time.Duration) func(*proxy.Config) {
	return func(cfg *proxy.Config) { cfg.UpstreamIdleTimeout = d }
}

func withTestLogger(buf *syncLogBuf) func(*proxy.Config) {
	return func(cfg *proxy.Config) {
		cfg.Logger = slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	}
}

func callErrorNormalized(call map[string]any) string {
	errObj, _ := normalized(call)["error"].(map[string]any)
	return fmtString(errObj["normalized"])
}

func countProxyResponseLines(log string) int {
	n := 0
	for _, line := range strings.Split(log, "\n") {
		if strings.Contains(line, `msg="proxy response"`) {
			n++
		}
	}
	return n
}

func waitMinCalls(t *testing.T, controlURL, session string, minCalls int) map[string]any {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, controlURL+"/sessions/"+session+"/wait?quiet=20ms&timeout=3s&min_calls="+strconv.Itoa(minCalls), nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("session json: %v body=%s", err, raw)
	}
	return out
}

func TestUpstreamHeaderTimeoutResponsesAndChat(t *testing.T) {
	logBuf := &syncLogBuf{}
	p := startSpecProxy(t, "warn", "relaxed", stallHeadersForever(),
		withHeaderTimeout(80*time.Millisecond), withTestLogger(logBuf))

	cfg := getConfig(t, p.controlURL)
	if fmtString(cfg["upstream_header_timeout"]) != "80ms" {
		t.Fatalf("config header timeout=%v", cfg["upstream_header_timeout"])
	}
	if fmtString(cfg["upstream_idle_timeout"]) != "0s" {
		t.Fatalf("config idle timeout=%v", cfg["upstream_idle_timeout"])
	}

	for _, tc := range []struct {
		name, path string
		body       []byte
	}{
		{"responses", "/v1/responses", responsesJSONBody},
		{"responses_stream", "/v1/responses", responsesStreamBody},
		{"chat", "/v1/chat/completions", chatJSONBody},
		{"chat_stream", "/v1/chat/completions", chatStreamBody},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sid := newSession()
			before := countProxyResponseLines(logBuf.String())
			status, _, raw, sess := postRaw(t, p, tc.path, sid, nil, tc.body)
			if status != http.StatusGatewayTimeout {
				t.Fatalf("status=%d body=%s", status, raw)
			}
			if openaiErrorType(t, raw) != "upstream_timeout" {
				t.Fatalf("client body=%s", raw)
			}
			call := lastCall(t, sess)
			if call["http_status"] != float64(http.StatusGatewayTimeout) {
				t.Fatalf("http_status=%v", call["http_status"])
			}
			if call["upstream_called"] != true {
				t.Fatalf("upstream_called=%v", call["upstream_called"])
			}
			if callErrorNormalized(call) != "upstream_timeout" {
				t.Fatalf("error=%v", normalized(call)["error"])
			}
			errObj, _ := normalized(call)["error"].(map[string]any)
			if fmtString(errObj["type"]) != "upstream_timeout" || fmtString(errObj["source"]) != "proxy" {
				t.Fatalf("error record=%v", errObj)
			}
			if call["client_aborted"] == true {
				t.Fatalf("header timeout marked client_aborted")
			}
			after := countProxyResponseLines(logBuf.String())
			if after-before != 1 {
				t.Fatalf("proxy response lines=%d log=%s", after-before, logBuf.String())
			}
			var found bool
			for _, line := range strings.Split(logBuf.String(), "\n") {
				if strings.Contains(line, `msg="proxy response"`) && strings.Contains(line, "status=504") && strings.Contains(line, "call_id="+fmtString(call["id"])) {
					found = true
					for _, want := range []string{"seq=", "spec=request_only"} {
						if !strings.Contains(line, want) {
							t.Fatalf("missing %s: %s", want, line)
						}
					}
				}
			}
			if !found {
				t.Fatalf("missing 504 proxy response for %s\n%s", tc.name, logBuf.String())
			}
		})
	}
}

func TestUpstreamIdleTimeoutStreamAndNonStream(t *testing.T) {
	for _, tc := range []struct {
		name, path string
		body       []byte
		upstream   http.HandlerFunc
		stream     bool
	}{
		{"responses_stream", "/v1/responses", responsesStreamBody, stallAfterFirstResponsesSSE(), true},
		{"chat_stream", "/v1/chat/completions", chatStreamBody, stallAfterFirstChatSSE(), true},
		{"responses_json", "/v1/responses", responsesJSONBody, stallAfterJSONPrefix(), false},
		{"chat_json", "/v1/chat/completions", chatJSONBody, stallAfterJSONPrefix(), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := startSpecProxy(t, "warn", "relaxed", tc.upstream, withIdleTimeout(80*time.Millisecond))
			sid := newSession()
			status, _, raw, sess := postRaw(t, p, tc.path, sid, nil, tc.body)
			if status != http.StatusOK {
				t.Fatalf("status=%d body=%s", status, raw)
			}
			if tc.stream && !bytes.Contains(raw, []byte("chunk-one")) {
				t.Fatalf("partial stream missing: %s", raw)
			}
			if !tc.stream && !bytes.Contains(raw, []byte(`{"id":"partial"`)) {
				t.Fatalf("partial json missing: %s", raw)
			}
			call := lastCall(t, sess)
			if call["http_status"] != float64(http.StatusOK) {
				t.Fatalf("http_status=%v", call["http_status"])
			}
			if callErrorNormalized(call) != "upstream_idle_timeout" {
				t.Fatalf("error=%v", normalized(call)["error"])
			}
			errObj, _ := normalized(call)["error"].(map[string]any)
			if errObj["mid_stream"] == true {
				t.Fatalf("mid_stream set: %v", errObj)
			}
			if fmtString(errObj["source"]) != "proxy" {
				t.Fatalf("source=%v", errObj)
			}
			if call["client_aborted"] == true {
				t.Fatalf("idle timeout marked client_aborted")
			}
			if tc.stream && normalized(call)["stream_truncated"] != true {
				t.Fatalf("expected stream_truncated: %+v", normalized(call))
			}
			if !tc.stream && normalized(call)["stream_truncated"] == true {
				t.Fatalf("partial JSON idle timeout set stream_truncated: %+v", normalized(call))
			}
		})
	}
}

func TestUpstreamTimeoutZeroMeansDisabled(t *testing.T) {
	p := startSpecProxy(t, "off", "relaxed", stallHeadersThenOK(120*time.Millisecond),
		withHeaderTimeout(0), withIdleTimeout(0))
	cfg := getConfig(t, p.controlURL)
	if fmtString(cfg["upstream_header_timeout"]) != "0s" || fmtString(cfg["upstream_idle_timeout"]) != "0s" {
		t.Fatalf("config=%v", cfg)
	}
	sid := newSession()
	status, _, raw, sess := postRaw(t, p, "/v1/responses", sid, nil, responsesJSONBody)
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, raw)
	}
	if callErrorNormalized(lastCall(t, sess)) != "" {
		t.Fatalf("unexpected error: %v", normalized(lastCall(t, sess))["error"])
	}
}

func TestUpstreamTimeoutWithinLimitDoesNotFire(t *testing.T) {
	p := startSpecProxy(t, "off", "relaxed", stallHeadersThenOK(40*time.Millisecond),
		withHeaderTimeout(250*time.Millisecond), withIdleTimeout(250*time.Millisecond))
	sid := newSession()
	status, _, raw, sess := postRaw(t, p, "/v1/chat/completions", sid, nil, chatJSONBody)
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, raw)
	}
	if callErrorNormalized(lastCall(t, sess)) != "" {
		t.Fatalf("unexpected error: %v", lastCall(t, sess))
	}
}

func TestUpstreamHeaderTimeoutRacesClientCancel(t *testing.T) {
	p := startSpecProxy(t, "off", "relaxed", stallHeadersForever(),
		withHeaderTimeout(400*time.Millisecond))
	sid := newSession()
	postCancelAfter(t, p.proxyURL, "/v1/responses", sid, responsesStreamBody, 40*time.Millisecond)
	call := lastCall(t, waitSession(t, p.controlURL, sid))
	if callErrorNormalized(call) != "client_aborted" {
		t.Fatalf("error=%v", normalized(call)["error"])
	}
	if call["client_aborted"] != true {
		t.Fatalf("call=%v", call)
	}
}

func TestUpstreamIdleTimeoutRacesClientCancel(t *testing.T) {
	p := startSpecProxy(t, "off", "relaxed", stallAfterFirstChatSSE(),
		withIdleTimeout(400*time.Millisecond))
	sid := newSession()
	postSSECloseAfter(t, p.proxyURL, "/v1/chat/completions", sid, "chunk-one", chatStreamBody)
	call := lastCall(t, waitSession(t, p.controlURL, sid))
	if call["client_aborted"] != true {
		t.Fatalf("expected client_aborted, got %v", call)
	}
	if callErrorNormalized(call) == "upstream_idle_timeout" {
		t.Fatalf("client cancel lost to idle timeout: %v", normalized(call)["error"])
	}
	if fmtString(call["abort_phase"]) != proxy.AbortPhaseAfterHeaders {
		t.Fatalf("abort_phase=%v", call["abort_phase"])
	}
}

func TestUpstreamTimeoutIsPerCallNotPerSession(t *testing.T) {
	p := startSpecProxy(t, "off", "relaxed", stallOrOK("X-Stall"),
		withHeaderTimeout(80*time.Millisecond))
	sid := newSession()
	status, _, raw, _ := postRaw(t, p, "/v1/responses", sid, nil, responsesJSONBody)
	if status != http.StatusOK {
		t.Fatalf("first call status=%d body=%s", status, raw)
	}
	time.Sleep(150 * time.Millisecond)
	started := time.Now()
	status, _, raw, sess := postRaw(t, p, "/v1/responses", sid, map[string]string{"X-Stall": "1"}, responsesJSONBody)
	elapsed := time.Since(started)
	if status != http.StatusGatewayTimeout {
		t.Fatalf("stalled call status=%d body=%s", status, raw)
	}
	if elapsed < 50*time.Millisecond {
		t.Fatalf("header timeout fired in %s; looks session-scoped", elapsed)
	}
	calls, _ := sess["calls"].([]any)
	if len(calls) != 2 {
		t.Fatalf("calls=%d sess=%v", len(calls), sess)
	}
	first, _ := calls[0].(map[string]any)
	second, _ := calls[1].(map[string]any)
	if callErrorNormalized(first) != "" {
		t.Fatalf("first call error=%v", normalized(first)["error"])
	}
	if callErrorNormalized(second) != "upstream_timeout" {
		t.Fatalf("second call error=%v", normalized(second)["error"])
	}
}

func TestUpstreamTimeoutConcurrentCallsIndependent(t *testing.T) {
	p := startSpecProxy(t, "off", "relaxed", stallOrOK("X-Stall"),
		withHeaderTimeout(80*time.Millisecond))
	sid := newSession()
	var wg sync.WaitGroup
	var stallStatus, okStatus int
	wg.Add(2)
	go func() {
		defer wg.Done()
		status, _, raw, _ := postRaw(t, p, "/v1/responses", sid, map[string]string{"X-Stall": "1"}, responsesJSONBody)
		stallStatus = status
		if status != http.StatusGatewayTimeout {
			t.Errorf("stall status=%d body=%s", status, raw)
		}
	}()
	go func() {
		defer wg.Done()
		status, _, raw, _ := postRaw(t, p, "/v1/responses", sid, nil, responsesJSONBody)
		okStatus = status
		if status != http.StatusOK {
			t.Errorf("ok status=%d body=%s", status, raw)
		}
	}()
	wg.Wait()
	if stallStatus != http.StatusGatewayTimeout || okStatus != http.StatusOK {
		t.Fatalf("stall=%d ok=%d", stallStatus, okStatus)
	}
	sess := waitMinCalls(t, p.controlURL, sid, 2)
	var sawTimeout, sawOK bool
	for _, item := range sess["calls"].([]any) {
		call, _ := item.(map[string]any)
		switch callErrorNormalized(call) {
		case "upstream_timeout":
			sawTimeout = true
		case "":
			sawOK = true
		}
	}
	if !sawTimeout || !sawOK {
		t.Fatalf("session calls=%v", sess["calls"])
	}
}

func TestDefaultConfigOmitsUpstreamTimeouts(t *testing.T) {
	p := startSpecProxy(t, "off", "relaxed", responsesOK())
	cfg := getConfig(t, p.controlURL)
	if fmtString(cfg["upstream_header_timeout"]) != "0s" || fmtString(cfg["upstream_idle_timeout"]) != "0s" {
		t.Fatalf("default config=%v", cfg)
	}
}

func TestWriteProxyErrorHeaderTimeoutUsesOpenAIShape(t *testing.T) {
	// End-to-end already covers the 504 body; this guards the decode-error
	// path that also calls doUpstream (invalid JSON still forwards).
	p := startSpecProxy(t, "off", "relaxed", stallHeadersForever(),
		withHeaderTimeout(80*time.Millisecond))
	sid := newSession()
	status, _, raw, sess := postRaw(t, p, "/v1/responses", sid, nil, []byte(`{not-json`))
	if status != http.StatusGatewayTimeout {
		t.Fatalf("status=%d body=%s", status, raw)
	}
	if openaiErrorType(t, raw) != "upstream_timeout" {
		t.Fatalf("body=%s", raw)
	}
	if callErrorNormalized(lastCall(t, sess)) != "upstream_timeout" {
		t.Fatalf("error=%v", lastCall(t, sess))
	}
}

func TestUpstreamIdleTimeoutDoesNotUseCanceledContextAfterHeaders(t *testing.T) {
	// A complete stream that finishes inside the idle window must not be
	// truncated just because a header-timeout child context was created.
	p := startSpecProxy(t, "off", "relaxed", responsesStreamWithDelay(20*time.Millisecond),
		withHeaderTimeout(500*time.Millisecond), withIdleTimeout(500*time.Millisecond))
	sid := newSession()
	status, _, raw, sess := postRaw(t, p, "/v1/responses", sid, nil, responsesStreamBody)
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, raw)
	}
	call := lastCall(t, sess)
	if normalized(call)["stream_truncated"] == true {
		t.Fatalf("complete stream truncated: %+v", normalized(call))
	}
	if callErrorNormalized(call) != "" {
		t.Fatalf("error=%v", normalized(call)["error"])
	}
}
