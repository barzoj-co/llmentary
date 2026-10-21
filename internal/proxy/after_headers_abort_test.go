// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClientAbortedAfterHeadersDecision(t *testing.T) {
	t.Parallel()
	closedPipe := io.ErrClosedPipe
	canceled := context.Canceled
	network := io.ErrUnexpectedEOF
	cases := []struct {
		name         string
		stream       bool
		complete     bool
		clientGone   bool
		copyWriteErr bool
		copyErr      error
		want         bool
	}{
		{
			name:         "incomplete stream write error without context cancel",
			stream:       true,
			copyWriteErr: true,
			copyErr:      closedPipe,
			want:         true,
		},
		{
			name:       "incomplete stream client gone",
			stream:     true,
			clientGone: true,
			want:       true,
		},
		{
			name:    "incomplete stream canceled copy",
			stream:  true,
			copyErr: canceled,
			want:    true,
		},
		{
			name:   "incomplete stream upstream hangup is not abort",
			stream: true,
		},
		{
			name:    "incomplete stream network read is not abort",
			stream:  true,
			copyErr: network,
		},
		{
			name:       "complete stream client close after terminal",
			stream:     true,
			complete:   true,
			clientGone: true,
		},
		{
			name:         "complete stream write error after terminal",
			stream:       true,
			complete:     true,
			clientGone:   true,
			copyWriteErr: true,
			copyErr:      closedPipe,
		},
		{
			name:         "non-stream write error without context cancel",
			copyWriteErr: true,
			copyErr:      closedPipe,
			want:         true,
		},
		{
			name:    "non-stream canceled body",
			copyErr: canceled,
			want:    true,
		},
		{
			name:       "non-stream complete body then client gone",
			clientGone: true,
		},
		{
			name:    "non-stream network read is not abort",
			copyErr: network,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := clientAbortedAfterHeaders(tc.stream, tc.complete, tc.clientGone, tc.copyWriteErr, tc.copyErr)
			if got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestFinishCallPublishesAfterHeadersAbortOnWriteError(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	s := newAbortTestServer(t, store)
	sid := "after-headers-write-err"
	raw := []byte(`{"model":"gpt-fixture","input":"hi","stream":true}`)
	started := time.Now().UTC()
	_, callID := store.Begin(sid, Call{
		StartedAt:  started,
		APIFamily:  "openai.responses",
		RawRequest: raw,
		RequestIDs: map[string]string{},
	})

	body := responsesAbortChunk("chunk-one")
	rec := newSSERecorder(started)
	rec.Write([]byte(body))
	first := started
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(raw))
	ex := &proxyExchange{
		s:         s,
		r:         req, // context is live; write error is the abort signal
		rawReq:    raw,
		session:   sessionInfo{ID: sid, Source: "header"},
		callID:    callID,
		startedAt: started,
	}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader("")),
	}
	ex.finishCall(resp, []byte(body), &first, rec, io.ErrClosedPipe, true, true)

	session, ok := store.Lookup(sid)
	if !ok || len(session.Calls) != 1 {
		t.Fatalf("session=%+v ok=%v", session, ok)
	}
	call := session.Calls[0]
	if !call.ClientAborted || call.AbortPhase != AbortPhaseAfterHeaders {
		t.Fatalf("abort fields missing on settled call: %+v", call)
	}
	if !call.Normalized.ClientAborted || call.Normalized.FinishReason != "cancelled" {
		t.Fatalf("normalized=%+v", call.Normalized)
	}
	if call.HTTPStatus != http.StatusOK {
		t.Fatalf("http_status=%d", call.HTTPStatus)
	}
	if !strings.Contains(call.Normalized.OutputText, "chunk-one") {
		t.Fatalf("output_text=%q", call.Normalized.OutputText)
	}
}

func TestCopyAndFinishPublishesAfterHeadersAbortOnClientWriteError(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	s := newAbortTestServer(t, store)
	sid := "copy-write-err"
	raw := []byte(`{"model":"gpt-fixture","input":"hi","stream":true}`)
	started := time.Now().UTC()
	_, callID := store.Begin(sid, Call{
		StartedAt:  started,
		APIFamily:  "openai.responses",
		RawRequest: raw,
		RequestIDs: map[string]string{},
	})

	created := "event: response.created\ndata: {\"type\":\"response.created\",\"sequence_number\":0}\n\n"
	delta := "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"chunk-one\"}\n\n"
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(raw))
	ex := &proxyExchange{
		s:         s,
		r:         req,
		rawReq:    raw,
		session:   sessionInfo{ID: sid, Source: "header"},
		callID:    callID,
		startedAt: started,
	}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(&splitReader{chunks: [][]byte{[]byte(created), []byte(delta)}}),
	}
	w := &failNthWrite{ResponseWriter: httptest.NewRecorder(), n: 2}
	if err := ex.copyAndFinish(w, resp); err != nil {
		t.Fatal(err)
	}

	session, ok := store.Lookup(sid)
	if !ok || len(session.Calls) != 1 {
		t.Fatalf("session=%+v ok=%v", session, ok)
	}
	call := session.Calls[0]
	if !call.ClientAborted || call.AbortPhase != AbortPhaseAfterHeaders || !call.Normalized.ClientAborted {
		t.Fatalf("settled call missing abort fields: %+v", call)
	}
	if call.Normalized.FinishReason != "cancelled" {
		t.Fatalf("finish_reason=%q", call.Normalized.FinishReason)
	}
	if call.HTTPStatus != http.StatusOK {
		t.Fatalf("http_status=%d", call.HTTPStatus)
	}
}

func TestFinishCallDoesNotAbortCompleteResponsesStream(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	s := newAbortTestServer(t, store)
	sid := "complete-then-close"
	raw := []byte(`{"model":"gpt-fixture","input":"hi","stream":true}`)
	started := time.Now().UTC()
	_, callID := store.Begin(sid, Call{
		StartedAt:  started,
		APIFamily:  "openai.responses",
		RawRequest: raw,
		RequestIDs: map[string]string{},
	})

	body := responsesAbortChunk("hi") + "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"status\":\"completed\",\"output\":[]}}\n\n"
	rec := newSSERecorder(started)
	rec.Write([]byte(body))
	first := started
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(raw)).WithContext(ctx)
	ex := &proxyExchange{
		s:         s,
		r:         req,
		rawReq:    raw,
		session:   sessionInfo{ID: sid, Source: "header"},
		callID:    callID,
		startedAt: started,
	}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader("")),
	}
	ex.finishCall(resp, []byte(body), &first, rec, io.ErrClosedPipe, true, true)

	session, ok := store.Lookup(sid)
	if !ok || len(session.Calls) != 1 {
		t.Fatalf("session=%+v ok=%v", session, ok)
	}
	call := session.Calls[0]
	if call.ClientAborted || call.Normalized.ClientAborted {
		t.Fatalf("complete stream marked aborted: %+v", call)
	}
	if call.AbortPhase != "" {
		t.Fatalf("abort_phase=%q", call.AbortPhase)
	}
}

func newAbortTestServer(t *testing.T, store *MemoryStore) *Server {
	t.Helper()
	cfg := Config{
		ListenAddr:     ":0",
		ControlAddr:    "127.0.0.1:0",
		SpecValidation: "off",
		SpecProfile:    "relaxed",
	}
	s, err := NewServer(cfg, store, TolerantParser{})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func responsesAbortChunk(text string) string {
	return "event: response.created\ndata: {\"type\":\"response.created\",\"sequence_number\":0}\n\n" +
		"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"" + text + "\"}\n\n"
}

type splitReader struct {
	chunks [][]byte
	i      int
}

func (r *splitReader) Read(p []byte) (int, error) {
	if r.i >= len(r.chunks) {
		return 0, io.EOF
	}
	n := copy(p, r.chunks[r.i])
	r.i++
	if n < len(r.chunks[r.i-1]) {
		return n, errors.New("short test buffer")
	}
	return n, nil
}

type failNthWrite struct {
	http.ResponseWriter
	n      int
	writes int
}

func (w *failNthWrite) Write(p []byte) (int, error) {
	w.writes++
	if w.n > 0 && w.writes >= w.n {
		return 0, io.ErrClosedPipe
	}
	return w.ResponseWriter.Write(p)
}

func (w *failNthWrite) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
