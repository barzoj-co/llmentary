// SPDX-License-Identifier: AGPL-3.0-only

package proxy_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/barzoj-co/llmentary/internal/proxy"
)

const delayedTerminator = 300 * time.Millisecond

func holdUntilCancel(r *http.Request, hold time.Duration) {
	timer := time.NewTimer(hold)
	defer timer.Stop()
	select {
	case <-r.Context().Done():
	case <-timer.C:
	}
}

func chatStreamWithDelay(delay time.Duration) http.HandlerFunc {
	chunk1 := `data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1,"model":"gpt-fixture","choices":[{"index":0,"delta":{"role":"assistant","content":"hi"},"finish_reason":null}]}` + "\n\n"
	chunk2 := `data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1,"model":"gpt-fixture","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n"
	done := "data: [DONE]\n\n"
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		_, _ = io.WriteString(w, chunk1)
		if flusher != nil {
			flusher.Flush()
		}
		_, _ = io.WriteString(w, chunk2)
		_, _ = io.WriteString(w, done)
		if flusher != nil {
			flusher.Flush()
		}
		// Delay the chunked terminator so a client close after [DONE]
		// cancels the inbound context while the copy is still reading.
		holdUntilCancel(r, delay)
	}
}

func chatStreamAbortAfterFirst() http.HandlerFunc {
	chunk1 := `data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1,"model":"gpt-fixture","choices":[{"index":0,"delta":{"role":"assistant","content":"chunk-one"},"finish_reason":null}]}` + "\n\n"
	more := strings.Repeat(`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1,"model":"gpt-fixture","choices":[{"index":0,"delta":{"content":"x"},"finish_reason":null}]}`+"\n\n", 64)
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		_, _ = io.WriteString(w, chunk1)
		if flusher != nil {
			flusher.Flush()
		}
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
		_, _ = io.WriteString(w, more)
		if flusher != nil {
			flusher.Flush()
		}
	}
}

func responsesStreamWithDelay(delay time.Duration) http.HandlerFunc {
	return responsesTerminalStream("response.completed", "completed", delay)
}

func responsesTerminalStream(event, status string, delay time.Duration) http.HandlerFunc {
	created := "event: response.created\ndata: {\"type\":\"response.created\",\"sequence_number\":0}\n\n"
	delta := "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n"
	terminal := fmt.Sprintf("event: %s\ndata: {\"type\":%q,\"sequence_number\":2,\"response\":{\"id\":\"resp_1\",\"created_at\":1,\"model\":\"gpt-fixture\",\"object\":\"response\",\"output\":[{\"id\":\"m\",\"type\":\"message\",\"role\":\"assistant\",\"status\":%q,\"content\":[{\"type\":\"output_text\",\"text\":\"hi\",\"annotations\":[],\"logprobs\":[]}]}],\"parallel_tool_calls\":true,\"status\":%q,\"tool_choice\":\"auto\",\"tools\":[]}}\n\n", event, event, status, status)
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		_, _ = io.WriteString(w, created)
		_, _ = io.WriteString(w, delta)
		_, _ = io.WriteString(w, terminal)
		if flusher != nil {
			flusher.Flush()
		}
		holdUntilCancel(r, delay)
	}
}

func postSSECloseAfter(t *testing.T, proxyURL, path, session, marker string, body []byte) {
	t.Helper()
	u, err := url.Parse(proxyURL + path)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("tcp", u.Host)
	if err != nil {
		t.Fatal(err)
	}
	req := fmt.Sprintf("POST %s HTTP/1.1\r\nHost: %s\r\nContent-Type: application/json\r\nContent-Length: %d\r\n%s: %s\r\nConnection: close\r\n\r\n%s",
		u.RequestURI(), u.Host, len(body), proxy.SessionHeaderName, session, body)
	if _, err := conn.Write([]byte(req)); err != nil {
		_ = conn.Close()
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 4096)
	var got []byte
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			got = append(got, buf[:n]...)
			if sseFrameComplete(got, marker) {
				_ = conn.Close()
				return
			}
		}
		if err != nil {
			_ = conn.Close()
			if !sseFrameComplete(got, marker) {
				t.Fatalf("closed without complete %q frame: %v body=%q", marker, err, got)
			}
			return
		}
	}
}

func sseFrameComplete(buf []byte, marker string) bool {
	idx := bytes.Index(buf, []byte(marker))
	if idx < 0 {
		return false
	}
	rest := buf[idx:]
	return bytes.Contains(rest, []byte("\n\n")) || bytes.Contains(rest, []byte("\r\n\r\n"))
}

func waitSession(t *testing.T, controlURL, session string) map[string]any {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, controlURL+"/sessions/"+session+"/wait?quiet=20ms&timeout=3s", nil)
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

func TestNonStreamClientCloseAfterBodyNotAborted(t *testing.T) {
	p := startSpecProxy(t, "off", "relaxed", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"id":"c","model":"gpt-fixture","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	})
	sid := newSession()
	status, _, _ := postJSON(t, p.proxyURL+"/v1/chat/completions", map[string]string{
		proxy.SessionHeaderName: sid,
	}, []byte(`{"model":"gpt-fixture","messages":[{"role":"user","content":"hi"}]}`))
	if status != http.StatusOK {
		t.Fatalf("status=%d", status)
	}
	n := normalized(lastCall(t, waitSession(t, p.controlURL, sid)))
	if n["client_aborted"] == true {
		t.Fatalf("complete non-stream marked client_aborted: %+v", n)
	}
	if n["finish_reason"] != "stop" {
		t.Fatalf("finish_reason=%v", n["finish_reason"])
	}
}

func TestClientAbortedFalseAfterDone(t *testing.T) {
	const loops = 20
	p := startSpecProxy(t, "off", "relaxed", chatStreamWithDelay(delayedTerminator))
	body := []byte(`{"model":"gpt-fixture","messages":[{"role":"user","content":"hi"}],"stream":true}`)
	for i := 0; i < loops; i++ {
		sid := fmt.Sprintf("done-%d", i)
		postSSECloseAfter(t, p.proxyURL, "/v1/chat/completions", sid, "data: [DONE]", body)
		n := normalized(lastCall(t, waitSession(t, p.controlURL, sid)))
		if n["client_aborted"] == true {
			t.Fatalf("loop %d: client_aborted=true after [DONE]", i)
		}
		if n["finish_reason"] != "stop" {
			t.Fatalf("loop %d: finish_reason=%v", i, n["finish_reason"])
		}
	}
}

func TestClientAbortedFalseAfterResponsesCompleted(t *testing.T) {
	p := startSpecProxy(t, "off", "relaxed", responsesStreamWithDelay(delayedTerminator))
	body := []byte(`{"model":"gpt-fixture","input":"hi","stream":true}`)
	for i := 0; i < 20; i++ {
		sid := fmt.Sprintf("resp-done-%d", i)
		postSSECloseAfter(t, p.proxyURL, "/v1/responses", sid, "response.completed", body)
		n := normalized(lastCall(t, waitSession(t, p.controlURL, sid)))
		if n["client_aborted"] == true {
			t.Fatalf("loop %d: client_aborted=true after response.completed", i)
		}
		if n["finish_reason"] != "stop" {
			t.Fatalf("loop %d: finish_reason=%v", i, n["finish_reason"])
		}
	}
}

func TestClientAbortedFalseAfterResponsesTerminalEvents(t *testing.T) {
	cases := []struct {
		event  string
		status string
	}{
		{"response.failed", "failed"},
		{"response.incomplete", "incomplete"},
	}
	body := []byte(`{"model":"gpt-fixture","input":"hi","stream":true}`)
	for _, tc := range cases {
		t.Run(tc.event, func(t *testing.T) {
			p := startSpecProxy(t, "off", "relaxed", responsesTerminalStream(tc.event, tc.status, delayedTerminator))
			sid := "resp-" + strings.ReplaceAll(tc.event, ".", "-")
			postSSECloseAfter(t, p.proxyURL, "/v1/responses", sid, tc.event, body)
			n := normalized(lastCall(t, waitSession(t, p.controlURL, sid)))
			if n["client_aborted"] == true {
				t.Fatalf("client_aborted=true after %s", tc.event)
			}
		})
	}
}

func responsesStreamAbortAfterFirst() http.HandlerFunc {
	created := "event: response.created\ndata: {\"type\":\"response.created\",\"sequence_number\":0}\n\n"
	delta := "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"chunk-one\"}\n\n"
	more := strings.Repeat("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"x\"}\n\n", 64)
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		_, _ = io.WriteString(w, created)
		_, _ = io.WriteString(w, delta)
		if flusher != nil {
			flusher.Flush()
		}
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
		_, _ = io.WriteString(w, more)
		if flusher != nil {
			flusher.Flush()
		}
	}
}

func assertAfterHeadersAbort(t *testing.T, call map[string]any, wantText string) {
	t.Helper()
	n := normalized(call)
	if n["client_aborted"] != true {
		t.Fatalf("client_aborted=%v", n["client_aborted"])
	}
	if call["client_aborted"] != true {
		t.Fatalf("top-level client_aborted=%v", call["client_aborted"])
	}
	if fmtString(call["abort_phase"]) != proxy.AbortPhaseAfterHeaders {
		t.Fatalf("abort_phase=%v", call["abort_phase"])
	}
	if call["http_status"] != float64(http.StatusOK) {
		t.Fatalf("http_status=%v want 200", call["http_status"])
	}
	if n["finish_reason"] != "cancelled" {
		t.Fatalf("finish_reason=%v", n["finish_reason"])
	}
	if call["upstream_called"] != true {
		t.Fatalf("upstream_called=%v", call["upstream_called"])
	}
	errObj, _ := n["error"].(map[string]any)
	if fmtString(errObj["normalized"]) == "network" {
		t.Fatalf("network error: %v", n["error"])
	}
	if errObj["mid_stream"] == true {
		t.Fatalf("mid_stream set: %v", n["error"])
	}
	if wantText != "" && !strings.Contains(fmtString(n["output_text"]), wantText) {
		t.Fatalf("output_text=%q want %q", n["output_text"], wantText)
	}
}

func chatStreamAbortAfterMidStreamError() http.HandlerFunc {
	chunk1 := `data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1,"model":"gpt-fixture","choices":[{"index":0,"delta":{"role":"assistant","content":"chunk-one"},"finish_reason":null}]}` + "\n\n"
	errEvt := `data: {"error":{"type":"too_many_requests","code":"no_capacity","message":"try later"}}` + "\n\n"
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		_, _ = io.WriteString(w, chunk1)
		_, _ = io.WriteString(w, errEvt)
		if flusher != nil {
			flusher.Flush()
		}
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}
}

func TestClientAbortAfterMidStreamErrorOmitsMidStream(t *testing.T) {
	p := startSpecProxy(t, "off", "relaxed", chatStreamAbortAfterMidStreamError())
	body := []byte(`{"model":"gpt-fixture","messages":[{"role":"user","content":"hi"}],"stream":true}`)
	sid := newSession()
	postSSECloseAfter(t, p.proxyURL, "/v1/chat/completions", sid, "try later", body)
	call := lastCall(t, waitSession(t, p.controlURL, sid))
	assertAfterHeadersAbort(t, call, "chunk-one")
	errObj, _ := normalized(call)["error"].(map[string]any)
	if errObj == nil {
		t.Fatalf("missing provider error: %+v", normalized(call))
	}
	if fmtString(errObj["type"]) != "too_many_requests" || fmtString(errObj["code"]) != "no_capacity" ||
		!strings.Contains(fmtString(errObj["message"]), "try later") {
		t.Fatalf("provider error=%v", errObj)
	}
	if errObj["mid_stream"] == true {
		t.Fatalf("client abort must omit mid_stream: %v", errObj)
	}
}

func TestClientAbortedTrueBeforeDone(t *testing.T) {
	p := startSpecProxy(t, "off", "relaxed", chatStreamAbortAfterFirst())
	body := []byte(`{"model":"gpt-fixture","messages":[{"role":"user","content":"hi"}],"stream":true}`)
	sid := newSession()
	postSSECloseAfter(t, p.proxyURL, "/v1/chat/completions", sid, "chunk-one", body)
	call := lastCall(t, waitSession(t, p.controlURL, sid))
	assertAfterHeadersAbort(t, call, "chunk-one")
}

func TestClientCancelAfterHeadersResponses(t *testing.T) {
	p := startSpecProxy(t, "off", "relaxed", responsesStreamAbortAfterFirst())
	body := []byte(`{"model":"gpt-fixture","input":"tell a long story","stream":true}`)
	sid := newSession()
	postSSECloseAfter(t, p.proxyURL, "/v1/responses", sid, "chunk-one", body)
	assertAfterHeadersAbort(t, lastCall(t, waitSession(t, p.controlURL, sid)), "chunk-one")
}

func TestClientCancelAfterHeadersLogsProxyResponse(t *testing.T) {
	logBuf := &syncLogBuf{}
	p := startSpecProxy(t, "off", "relaxed", chatStreamAbortAfterFirst(), func(cfg *proxy.Config) {
		cfg.Logger = slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	})
	sid := newSession()
	body := []byte(`{"model":"gpt-fixture","messages":[{"role":"user","content":"hi"}],"stream":true}`)
	postSSECloseAfter(t, p.proxyURL, "/v1/chat/completions", sid, "chunk-one", body)
	_ = waitSession(t, p.controlURL, sid)
	var n int
	for _, line := range strings.Split(logBuf.String(), "\n") {
		if strings.Contains(line, `msg="proxy response"`) && strings.Contains(line, "client_aborted=true") {
			n++
			for _, want := range []string{"status=200", "seq=1", "spec=", "phase=after_headers"} {
				if !strings.Contains(line, want) {
					t.Fatalf("abort log missing %s: %s", want, line)
				}
			}
		}
	}
	if n != 1 {
		t.Fatalf("proxy response abort lines=%d log=%s", n, logBuf.String())
	}
}

func TestCompleteStreamClientCloseHasNoOTelError(t *testing.T) {
	p := startSpecProxy(t, "off", "relaxed", chatStreamWithDelay(delayedTerminator))
	body := []byte(`{"model":"gpt-fixture","messages":[{"role":"user","content":"hi"}],"stream":true}`)
	sid := newSession()
	postSSECloseAfter(t, p.proxyURL, "/v1/chat/completions", sid, "data: [DONE]", body)
	_ = waitSession(t, p.controlURL, sid)
	raw := fetchOTLP(t, p.controlURL, sid)
	var traces map[string]any
	if err := json.Unmarshal(raw, &traces); err != nil {
		t.Fatal(err)
	}
	span := onlySpan(t, traces)
	if status, _ := span["status"].(map[string]any); status != nil {
		if fmt.Sprint(status["code"]) == "2" || strings.EqualFold(fmt.Sprint(status["code"]), "STATUS_CODE_ERROR") {
			t.Fatalf("span error status: %+v", status)
		}
	}
	attrs := attrsByKey(span)
	if _, ok := attrs["error.type"]; ok {
		t.Fatalf("error.type present: %+v", attrs["error.type"])
	}
}
