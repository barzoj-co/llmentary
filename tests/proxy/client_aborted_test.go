// SPDX-License-Identifier: AGPL-3.0-only

package proxy_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
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
	created := "event: response.created\ndata: {\"type\":\"response.created\",\"sequence_number\":0}\n\n"
	delta := "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n"
	completed := "event: response.completed\ndata: {\"type\":\"response.completed\",\"sequence_number\":2,\"response\":{\"id\":\"resp_1\",\"created_at\":1,\"model\":\"gpt-fixture\",\"object\":\"response\",\"output\":[{\"id\":\"m\",\"type\":\"message\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"hi\",\"annotations\":[],\"logprobs\":[]}]}],\"parallel_tool_calls\":true,\"status\":\"completed\",\"tool_choice\":\"auto\",\"tools\":[]}}\n\n"
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		_, _ = io.WriteString(w, created)
		_, _ = io.WriteString(w, delta)
		_, _ = io.WriteString(w, completed)
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
			if bytes.Contains(got, []byte(marker)) {
				_ = conn.Close()
				return
			}
		}
		if err != nil {
			_ = conn.Close()
			if !bytes.Contains(got, []byte(marker)) {
				t.Fatalf("closed without %q: %v body=%q", marker, err, got)
			}
			return
		}
	}
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

func TestClientAbortedTrueBeforeDone(t *testing.T) {
	p := startSpecProxy(t, "off", "relaxed", chatStreamAbortAfterFirst())
	body := []byte(`{"model":"gpt-fixture","messages":[{"role":"user","content":"hi"}],"stream":true}`)
	sid := newSession()
	postSSECloseAfter(t, p.proxyURL, "/v1/chat/completions", sid, "chunk-one", body)
	n := normalized(lastCall(t, waitSession(t, p.controlURL, sid)))
	if n["client_aborted"] != true {
		t.Fatalf("expected client_aborted after early close: %+v", n)
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
