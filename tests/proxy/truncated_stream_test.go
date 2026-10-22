// SPDX-License-Identifier: AGPL-3.0-only

package proxy_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/barzoj-co/llmentary/internal/proxy"
)

const chatSSEHello = `data: {"id":"x","object":"chat.completion.chunk","created":1,"model":"gpt-fixture","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}` + "\n\n"

func hijackDropChunked(payload string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "no hijack", http.StatusInternalServerError)
			return
		}
		conn, bufrw, err := hj.Hijack()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		chunk := fmt.Sprintf("%x\r\n%s\r\n", len(payload), payload)
		_, _ = io.WriteString(bufrw, "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nTransfer-Encoding: chunked\r\n\r\n")
		_, _ = io.WriteString(bufrw, chunk)
		_ = bufrw.Flush()
	}
}

func hijackInvalidChunkLength(payload string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "no hijack", http.StatusInternalServerError)
			return
		}
		conn, bufrw, err := hj.Hijack()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		chunk := fmt.Sprintf("%x\r\n%s\r\n", len(payload), payload)
		_, _ = io.WriteString(bufrw, "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nTransfer-Encoding: chunked\r\n\r\n")
		_, _ = io.WriteString(bufrw, chunk)
		_, _ = io.WriteString(bufrw, "x\r\n")
		_ = bufrw.Flush()
	}
}

func hijackDropContentLength(body string, declared int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "no hijack", http.StatusInternalServerError)
			return
		}
		conn, bufrw, err := hj.Hijack()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_, _ = fmt.Fprintf(bufrw, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s", declared, body)
		_ = bufrw.Flush()
	}
}

func writeCompleteChunkedSSE(payload string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, payload)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
}

func writeCompleteChunkedSSEWithDone() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, chatSSEHello)
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}
}

func assertCallError(t *testing.T, call map[string]any, wantNorm, source string) {
	t.Helper()
	errObj, _ := normalized(call)["error"].(map[string]any)
	if fmtString(errObj["normalized"]) != wantNorm {
		t.Fatalf("error.normalized=%v want %s full=%v", errObj["normalized"], wantNorm, errObj)
	}
	if fmtString(errObj["source"]) != source {
		t.Fatalf("error.source=%v want %s", errObj["source"], source)
	}
	if fmtString(normalized(call)["finish_reason"]) != "error" {
		t.Fatalf("finish_reason=%v", normalized(call)["finish_reason"])
	}
}

func TestUpstreamDropChunkedIsBrokenAndRecorded(t *testing.T) {
	logBuf := &syncLogBuf{}
	p := startSpecProxy(t, "off", "relaxed", hijackDropChunked(chatSSEHello), withTestLogger(logBuf))
	sid := newSession()
	status, raw, sess, readErr := postRawAllowBroken(t, p, "/v1/chat/completions", sid, nil, chatStreamBody)
	if !isBrokenStreamErr(readErr) {
		t.Fatalf("client read succeeded on dropped stream: err=%v body=%q", readErr, raw)
	}
	if status != http.StatusOK && status != 0 {
		t.Fatalf("status=%d", status)
	}
	if !strings.Contains(string(raw), "Hello") {
		t.Fatalf("dropped stream did not forward upstream bytes: body=%q", raw)
	}
	call := lastCall(t, sess)
	if normalized(call)["stream_truncated"] != true {
		t.Fatalf("stream_truncated: %+v", normalized(call))
	}
	assertCallError(t, call, "stream_truncated", "upstream")
	if call["client_aborted"] == true {
		t.Fatal("upstream drop marked client_aborted")
	}
	var found bool
	for _, line := range strings.Split(logBuf.String(), "\n") {
		if strings.Contains(line, `msg="proxy response"`) && strings.Contains(line, "error=stream_truncated") && strings.Contains(line, "stream_truncated=true") {
			if strings.Contains(line, "Authorization") || strings.Contains(line, `"Hello"`) {
				t.Fatalf("log leaked body or header: %s", line)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("missing error/truncation on proxy response\n%s", logBuf.String())
	}
}

func TestInvalidChunkLengthIsBrokenAndRecorded(t *testing.T) {
	p := startSpecProxy(t, "off", "relaxed", hijackInvalidChunkLength(chatSSEHello))
	sid := newSession()
	_, raw, sess, readErr := postRawAllowBroken(t, p, "/v1/chat/completions", sid, nil, chatStreamBody)
	if !isBrokenStreamErr(readErr) {
		t.Fatalf("invalid chunk length ended cleanly: err=%v body=%q", readErr, raw)
	}
	call := lastCall(t, sess)
	assertCallError(t, call, "stream_truncated", "upstream")
	if call["client_aborted"] == true {
		t.Fatal("invalid chunk length marked client_aborted")
	}
}

func TestContentLengthCutShortIsBrokenAndRecorded(t *testing.T) {
	p := startSpecProxy(t, "off", "relaxed", hijackDropContentLength(`{"id":"partial"`, 200))
	sid := newSession()
	_, raw, sess, readErr := postRawAllowBroken(t, p, "/v1/chat/completions", sid, nil, chatJSONBody)
	if !isBrokenStreamErr(readErr) {
		t.Fatalf("short Content-Length ended cleanly: err=%v body=%q", readErr, raw)
	}
	call := lastCall(t, sess)
	assertCallError(t, call, "stream_truncated", "upstream")
	if call["client_aborted"] == true {
		t.Fatal("short body marked client_aborted")
	}
}

func TestIdleTimeoutAbortsClientStream(t *testing.T) {
	logBuf := &syncLogBuf{}
	p := startSpecProxy(t, "off", "relaxed", stallAfterFirstChatSSE(),
		withIdleTimeout(80*time.Millisecond), withTestLogger(logBuf))
	sid := newSession()
	status, raw, sess, readErr := postRawAllowBroken(t, p, "/v1/chat/completions", sid, nil, chatStreamBody)
	if !isBrokenStreamErr(readErr) {
		t.Fatalf("idle timeout delivered a clean stream: err=%v body=%q", readErr, raw)
	}
	if status != http.StatusOK && status != 0 {
		t.Fatalf("status=%d", status)
	}
	call := lastCall(t, sess)
	assertCallError(t, call, "upstream_idle_timeout", "proxy")
	if normalized(call)["stream_truncated"] != true {
		t.Fatalf("expected stream_truncated: %+v", normalized(call))
	}
	var found bool
	for _, line := range strings.Split(logBuf.String(), "\n") {
		if strings.Contains(line, `msg="proxy response"`) && strings.Contains(line, "error=upstream_idle_timeout") && strings.Contains(line, "stream_truncated=true") {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing idle tokens on proxy response\n%s", logBuf.String())
	}
}

func TestCompleteDoneStreamStaysClean(t *testing.T) {
	logBuf := &syncLogBuf{}
	p := startSpecProxy(t, "off", "relaxed", writeCompleteChunkedSSEWithDone(), withTestLogger(logBuf))
	sid := newSession()
	status, _, raw, sess := postRaw(t, p, "/v1/chat/completions", sid, nil, chatStreamBody)
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, raw)
	}
	if !strings.Contains(string(raw), "[DONE]") {
		t.Fatalf("missing [DONE]: %s", raw)
	}
	call := lastCall(t, sess)
	if normalized(call)["stream_truncated"] == true {
		t.Fatalf("complete stream truncated: %+v", normalized(call))
	}
	if normalized(call)["error"] != nil {
		t.Fatalf("complete stream error: %v", normalized(call)["error"])
	}
	for _, line := range strings.Split(logBuf.String(), "\n") {
		if strings.Contains(line, `msg="proxy response"`) {
			if strings.Contains(line, "error=") || strings.Contains(line, "stream_truncated=") {
				t.Fatalf("clean call logged outcome tokens: %s", line)
			}
		}
	}
}

func TestCompleteChunkedWithoutDoneIsTruncatedNotError(t *testing.T) {
	p := startSpecProxy(t, "off", "relaxed", writeCompleteChunkedSSE(chatSSEHello))
	sid := newSession()
	status, _, raw, sess := postRaw(t, p, "/v1/chat/completions", sid, nil, chatStreamBody)
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, raw)
	}
	call := lastCall(t, sess)
	if normalized(call)["stream_truncated"] != true {
		t.Fatalf("expected stream_truncated only: %+v", normalized(call))
	}
	if normalized(call)["error"] != nil {
		t.Fatalf("complete chunked without [DONE] must not record an error: %v", normalized(call)["error"])
	}
	if call["client_aborted"] == true {
		t.Fatal("provider omit [DONE] marked client_aborted")
	}
}

func TestClientCancelStillAbortedNotUpstreamDrop(t *testing.T) {
	started := make(chan struct{})
	p := startSpecProxy(t, "off", "relaxed", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		_, _ = io.WriteString(w, chatSSEHello)
		if flusher != nil {
			flusher.Flush()
		}
		close(started)
		<-r.Context().Done()
	})
	sid := newSession()
	req, err := http.NewRequest(http.MethodPost, p.proxyURL+"/v1/chat/completions", strings.NewReader(string(chatStreamBody)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(proxy.SessionHeaderName, sid)
	ctx, cancel := context.WithCancel(context.Background())
	req = req.WithContext(ctx)
	errCh := make(chan error, 1)
	go func() {
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			errCh <- err
			return
		}
		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
		errCh <- nil
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream did not start")
	}
	cancel()
	<-errCh
	sess := waitMinCalls(t, p.controlURL, sid, 1)
	call := lastCall(t, sess)
	if call["client_aborted"] != true {
		t.Fatalf("want client_aborted: %+v", call)
	}
	errObj, _ := normalized(call)["error"].(map[string]any)
	if fmtString(errObj["normalized"]) == "stream_truncated" {
		t.Fatalf("client cancel recorded as upstream drop: %v", errObj)
	}
}

func TestTruncatedStreamDocs(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "proxy.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, n := range []string{
		"The client connection is then",
		"aborted so the app sees a broken stream",
		"error.normalized: stream_truncated",
		"error.source: upstream",
	} {
		if !strings.Contains(text, n) {
			t.Errorf("docs/proxy.md missing %q", n)
		}
	}
	dm, err := os.ReadFile(filepath.Join("..", "..", "docs", "data-model.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(dm), `error.normalized: "stream_truncated"`) {
		t.Error("docs/data-model.md missing stream_truncated error")
	}
	if !strings.Contains(string(dm), "`stream_truncated`") || !strings.Contains(string(dm), "Error normalization categories") {
		t.Error("docs/data-model.md missing stream_truncated in error categories")
	}
}
