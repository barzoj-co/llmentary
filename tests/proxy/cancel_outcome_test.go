// SPDX-License-Identifier: AGPL-3.0-only

package proxy_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/barzoj-co/llmentary/internal/proxy"
)

const outcomeSSEChunkTwo = `data: {"id":"x","object":"chat.completion.chunk","created":1,"model":"gpt-fixture","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":null}]}` + "\n\n"

// outcomeAssert collects every field mismatch and fails once with the full record.
type outcomeAssert struct{ fails []string }

func (a *outcomeAssert) eq(desc string, got, want any) {
	if fmt.Sprint(got) != fmt.Sprint(want) {
		a.fails = append(a.fails, fmt.Sprintf("%s=%v want %v", desc, got, want))
	}
}

func (a *outcomeAssert) wantTrue(desc string, got any) {
	if got != true {
		a.fails = append(a.fails, fmt.Sprintf("%s=%v want true", desc, got))
	}
}

func (a *outcomeAssert) wantFalse(desc string, got any) {
	if got == true {
		a.fails = append(a.fails, fmt.Sprintf("%s=%v want false/absent", desc, got))
	}
}

func (a *outcomeAssert) has(obj map[string]any, key, desc string) {
	if _, ok := obj[key]; !ok {
		a.fails = append(a.fails, fmt.Sprintf("%s: missing key %q", desc, key))
	}
}

func (a *outcomeAssert) notHas(obj map[string]any, key, desc string) {
	if _, ok := obj[key]; ok {
		a.fails = append(a.fails, fmt.Sprintf("%s: unexpected key %q", desc, key))
	}
}

func (a *outcomeAssert) notHasKey(raw []byte, key, desc string) {
	if rawJSONHasKey(raw, key) {
		a.fails = append(a.fails, fmt.Sprintf("%s: raw record has key %q", desc, key))
	}
}

func (a *outcomeAssert) count(desc string, got, want int) {
	if got != want {
		a.fails = append(a.fails, fmt.Sprintf("%s=%d want %d", desc, got, want))
	}
}

func (a *outcomeAssert) done(t *testing.T, call map[string]any) {
	t.Helper()
	if len(a.fails) == 0 {
		return
	}
	raw, _ := json.Marshal(call)
	t.Fatalf("outcome record mismatch: %s\nsettled call=%s", strings.Join(a.fails, "; "), raw)
}

// outcomeRecord settles the call through the control wait API, then re-reads the
// raw session so assertions run against the stored JSON record.
func outcomeRecord(t *testing.T, p proxyProc, sid string) (map[string]any, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, p.controlURL+"/sessions/"+sid+"/wait?timeout=5s&quiet=50ms&min_calls=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	wraw, err := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("wait status=%d body=%s", res.StatusCode, wraw)
	}
	raw := sessionRaw(t, p.controlURL, sid)
	var sess map[string]any
	if err := json.Unmarshal(raw, &sess); err != nil {
		t.Fatalf("session json: %v body=%s", err, raw)
	}
	return lastCall(t, sess), raw
}

func outcomePost(t *testing.T, p proxyProc, sid string, body []byte) (int, []byte, error) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, p.proxyURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(proxy.SessionHeaderName, sid)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	status := res.StatusCode
	raw, readErr := io.ReadAll(res.Body)
	_ = res.Body.Close()
	return status, raw, readErr
}

func outcomeStreamBody() []byte {
	return []byte(`{"model":"gpt-fixture","messages":[{"role":"user","content":"hi"}],"stream":true}`)
}

func outcomeCallJSON(t *testing.T, call map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(call)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func outcomeNormalizedJSON(t *testing.T, call map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(normalized(call))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func outcomeErrObj(call map[string]any) map[string]any {
	errObj, _ := normalized(call)["error"].(map[string]any)
	return errObj
}

func sseEventCount(call map[string]any) int {
	return len(asAnySlice(call["sse_events"]))
}

func TestOutcomeCompleteStream(t *testing.T) {
	p := startSpecProxy(t, "off", "relaxed", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		_, _ = io.WriteString(w, chatSSEHello)
		if flusher != nil {
			flusher.Flush()
		}
		_, _ = io.WriteString(w, outcomeSSEChunkTwo)
		if flusher != nil {
			flusher.Flush()
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	})
	sid := newSession()
	status, raw, readErr := outcomePost(t, p, sid, outcomeStreamBody())
	if readErr != nil {
		t.Fatalf("client read error on complete stream: %v body=%q", readErr, raw)
	}
	if status != http.StatusOK {
		t.Fatalf("client status=%d body=%s", status, raw)
	}
	if !strings.Contains(string(raw), "[DONE]") {
		t.Fatalf("client body missing [DONE]: %s", raw)
	}
	call, _ := outcomeRecord(t, p, sid)
	n := normalized(call)
	a := &outcomeAssert{}
	a.eq("outcome", fmtString(call["outcome"]), proxy.OutcomeCommitted)
	a.wantFalse("client_aborted", call["client_aborted"])
	a.wantFalse("normalized.client_aborted", n["client_aborted"])
	a.notHas(n, "error", "normalized")
	a.notHas(n, "stream_truncated", "normalized")
	a.has(call, "sse_events", "call")
	a.count("sse_events", sseEventCount(call), 3)
	a.wantTrue("upstream_called", call["upstream_called"])
	a.done(t, call)
}

func TestOutcomeCancelBeforeHeaders(t *testing.T) {
	received := make(chan struct{})
	p := startSpecProxy(t, "off", "relaxed", func(w http.ResponseWriter, r *http.Request) {
		close(received)
		<-r.Context().Done()
	})
	sid := newSession()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.proxyURL+"/v1/chat/completions", bytes.NewReader(outcomeStreamBody()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(proxy.SessionHeaderName, sid)
	done := make(chan error, 1)
	go func() {
		res, err := http.DefaultClient.Do(req)
		if res != nil {
			_, _ = io.Copy(io.Discard, res.Body)
			_ = res.Body.Close()
		}
		done <- err
	}()
	<-received
	cancel()
	if err := <-done; err == nil {
		t.Fatal("expected client cancel error")
	}
	call, _ := outcomeRecord(t, p, sid)
	n := normalized(call)
	errObj := outcomeErrObj(call)
	a := &outcomeAssert{}
	a.eq("outcome", fmtString(call["outcome"]), proxy.OutcomeCancelled)
	a.eq("abort_phase", fmtString(call["abort_phase"]), proxy.AbortPhaseBeforeHeaders)
	a.eq("http_status", call["http_status"], float64(0))
	a.wantTrue("upstream_called", call["upstream_called"])
	a.wantTrue("client_aborted", call["client_aborted"])
	a.wantTrue("normalized.client_aborted", n["client_aborted"])
	a.eq("normalized.finish_reason", fmtString(n["finish_reason"]), "cancelled")
	a.eq("error.normalized", fmtString(errObj["normalized"]), "client_aborted")
	a.eq("error.source", fmtString(errObj["source"]), "client")
	a.notHas(call, "sse_events", "call")
	a.notHasKey(outcomeCallJSON(t, call), "sse_events", "raw call")
	a.notHas(n, "usage", "normalized")
	a.notHasKey(outcomeNormalizedJSON(t, call), "usage", "raw normalized")
	a.done(t, call)
}

func TestOutcomeCancelAfterFirstChunk(t *testing.T) {
	sent := make(chan struct{})
	p := startSpecProxy(t, "off", "relaxed", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, chatSSEHello)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		close(sent)
		<-r.Context().Done()
	})
	sid := newSession()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.proxyURL+"/v1/chat/completions", bytes.NewReader(outcomeStreamBody()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(proxy.SessionHeaderName, sid)
	gotFrame := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			done <- err
			return
		}
		var got []byte
		buf := make([]byte, 64)
		for !bytes.Contains(got, []byte("\n\n")) {
			n, readErr := res.Body.Read(buf)
			got = append(got, buf[:n]...)
			if readErr != nil {
				_ = res.Body.Close()
				done <- fmt.Errorf("incomplete SSE frame %q: %w", got, readErr)
				return
			}
		}
		close(gotFrame)
		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
		done <- nil
	}()
	select {
	case <-gotFrame:
	case err := <-done:
		t.Fatalf("client failed before first SSE frame: %v", err)
	}
	<-sent
	cancel()
	<-done
	call, _ := outcomeRecord(t, p, sid)
	n := normalized(call)
	a := &outcomeAssert{}
	a.eq("outcome", fmtString(call["outcome"]), proxy.OutcomeCancelled)
	a.eq("abort_phase", fmtString(call["abort_phase"]), proxy.AbortPhaseAfterHeaders)
	a.eq("http_status", call["http_status"], float64(http.StatusOK))
	a.wantTrue("stream_truncated", n["stream_truncated"])
	a.notHas(n, "error", "normalized")
	a.notHasKey(outcomeNormalizedJSON(t, call), "error", "raw normalized")
	a.has(call, "sse_events", "call")
	a.count("sse_events", sseEventCount(call), 1)
	a.notHas(n, "usage", "normalized")
	a.notHasKey(outcomeNormalizedJSON(t, call), "usage", "raw normalized")
	a.done(t, call)
}

func TestOutcomeUpstreamDropMidStream(t *testing.T) {
	p := startSpecProxy(t, "off", "relaxed", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, chatSSEHello)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		hj, ok := w.(http.Hijacker)
		if !ok {
			return
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			return
		}
		_ = conn.Close()
	})
	sid := newSession()
	_, raw, readErr := outcomePost(t, p, sid, outcomeStreamBody())
	if !isBrokenStreamErr(readErr) {
		t.Fatalf("upstream drop delivered a clean stream: err=%v body=%q", readErr, raw)
	}
	call, _ := outcomeRecord(t, p, sid)
	n := normalized(call)
	errObj := outcomeErrObj(call)
	a := &outcomeAssert{}
	a.eq("outcome", fmtString(call["outcome"]), proxy.OutcomeFailed)
	a.eq("error.normalized", fmtString(errObj["normalized"]), "stream_truncated")
	a.eq("error.source", fmtString(errObj["source"]), "upstream")
	a.wantFalse("client_aborted", call["client_aborted"])
	a.wantFalse("normalized.client_aborted", n["client_aborted"])
	a.done(t, call)
}

func TestOutcomeUpstream500(t *testing.T) {
	p := startSpecProxy(t, "off", "relaxed", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":{"message":"boom","type":"server_error","code":"internal_error"}}`)
	})
	sid := newSession()
	status, raw, readErr := outcomePost(t, p, sid, outcomeStreamBody())
	if readErr != nil {
		t.Fatalf("client read error: %v body=%q", readErr, raw)
	}
	if status != http.StatusInternalServerError {
		t.Fatalf("client status=%d body=%s", status, raw)
	}
	call, _ := outcomeRecord(t, p, sid)
	errObj := outcomeErrObj(call)
	a := &outcomeAssert{}
	a.eq("outcome", fmtString(call["outcome"]), proxy.OutcomeFailed)
	a.eq("http_status", call["http_status"], float64(http.StatusInternalServerError))
	a.eq("error.source", fmtString(errObj["source"]), "upstream")
	a.wantFalse("client_aborted", call["client_aborted"])
	a.done(t, call)
}
