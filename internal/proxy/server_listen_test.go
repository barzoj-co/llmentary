// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestListenAndServeShutdownAndListenError(t *testing.T) {
	s := newAbortTestServer(t, NewMemoryStore())
	s.cfg.ListenAddr = "127.0.0.1:0"
	s.cfg.ControlAddr = "127.0.0.1:0"
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.ListenAndServe(ctx) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("shutdown: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ListenAndServe did not return after cancel")
	}

	bad := newAbortTestServer(t, NewMemoryStore())
	bad.cfg.ListenAddr = "127.0.0.1:1"
	bad.cfg.ControlAddr = "256.256.256.256:9"
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := bad.ListenAndServe(ctx); err == nil {
		t.Fatal("expected listen error")
	}
}

func TestNewServerNilStoreParserAndLogger(t *testing.T) {
	cfg := Config{ListenAddr: ":0", ControlAddr: "127.0.0.1:0", SpecValidation: "off", SpecProfile: "relaxed"}
	s, err := NewServer(cfg, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.store == nil || s.parser == nil {
		t.Fatal("defaults")
	}
	if s.logger() == nil {
		t.Fatal("logger")
	}
	s.cfg.Logger = nil
	if s.logger() == nil {
		t.Fatal("fallback logger")
	}
	cfgJSON := s.configJSON()
	if _, ok := cfgJSON["upstreams"].(map[string]string); !ok {
		t.Fatalf("config json=%v", cfgJSON)
	}
}

func TestRouteErrorsAndDefaults(t *testing.T) {
	s := newAbortTestServer(t, NewMemoryStore())
	if _, _, _, err := s.route("/u/"); err == nil || !strings.Contains(err.Error(), "unknown upstream") && !strings.Contains(err.Error(), "missing") {
		// /u/ splits to ["u", ""] so unknown empty name
		if err == nil {
			t.Fatal("expected route error")
		}
	}
	if _, _, _, err := s.route("/u/missing/v1"); err == nil || !strings.Contains(err.Error(), "unknown upstream") {
		t.Fatalf("missing: %v", err)
	}
	multi := newAbortTestServer(t, NewMemoryStore())
	if err := multi.cfg.AddUpstream("or=https://openrouter.ai/api/v1"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := multi.route("/v1/models"); err == nil || !strings.Contains(err.Error(), "bare proxy") {
		t.Fatalf("bare: %v", err)
	}
	named, rest, base, err := s.route("/u/openai/v1/models")
	if err != nil || named != "openai" || rest != "/v1/models" || base == nil {
		t.Fatalf("named %s %s %v %v", named, rest, base, err)
	}
}

func TestIsSSECopyFlushAndHopHeaders(t *testing.T) {
	t.Parallel()
	if !isSSE(http.Header{"Content-Type": []string{"text/event-stream"}}, nil) {
		t.Fatal("header")
	}
	if !isSSE(nil, []byte("data: hi\n\n")) {
		t.Fatal("data prefix")
	}
	if !isSSE(nil, []byte("ignored\nevent: x\n")) {
		t.Fatal("event")
	}
	if isSSE(nil, []byte("plain")) {
		t.Fatal("plain")
	}

	h := http.Header{
		"Connection":       []string{"Keep-Alive, Upgrade"},
		"Keep-Alive":       []string{"timeout=5"},
		"Upgrade":          []string{"websocket"},
		"X-Custom":         []string{"ok"},
		"Proxy-Connection": []string{"close"},
	}
	removeHopByHopHeaders(h)
	if h.Get("Keep-Alive") != "" || h.Get("Upgrade") != "" || h.Get("X-Custom") != "ok" {
		t.Fatalf("%v", h)
	}
	if retryAfter(http.Header{"Retry-After-Ms": []string{"10"}}) != "10" {
		t.Fatal("retry-after-ms")
	}
	if retryAfter(http.Header{"Retry-After": []string{"2"}}) != "2" {
		t.Fatal("retry-after")
	}
	if defaultString("", "x") != "x" || defaultString("y", "x") != "y" {
		t.Fatal("defaultString")
	}

	var n int64
	written, err, writeErr := copyAndFlush(&nopWriter{}, strings.NewReader("abc"), func(b []byte) { n += int64(len(b)) })
	if err != nil || writeErr || written != 3 || n != 3 {
		t.Fatalf("copy %d %v %v n=%d", written, err, writeErr, n)
	}
	_, err, writeErr = copyAndFlush(&errWriteWriter{}, strings.NewReader("abc"), nil)
	if err == nil || !writeErr {
		t.Fatalf("fail write %v %v", err, writeErr)
	}
}

func TestServeProxyUnknownPathAndBadSession(t *testing.T) {
	s := newAbortTestServer(t, NewMemoryStore())
	ts := httptest.NewServer(s.ProxyHandler())
	t.Cleanup(ts.Close)

	res, err := http.Get(ts.URL + "/not-a-real-api")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNotFound || !strings.Contains(string(raw), "llmentary_unknown_path") && !strings.Contains(string(raw), "unknown path") {
		t.Fatalf("unknown %d %s", res.StatusCode, raw)
	}

	res, err = http.Get(ts.URL + "/abc/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNotFound || !strings.Contains(string(raw), "/s/abc/") {
		t.Fatalf("hint %d %s", res.StatusCode, raw)
	}

	res, err = http.Get(ts.URL + "/s/%2F/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad session %d", res.StatusCode)
	}
}

func TestRecordRequestIDs(t *testing.T) {
	t.Parallel()
	call := &Call{RequestIDs: map[string]string{}}
	recordRequestIDs(call, http.Header{
		"X-Request-Id":    []string{"r1"},
		"Request-Id":      []string{"r2"},
		"Apim-Request-Id": []string{"a1"},
		"Content-Type":    []string{"application/json"},
	})
	if call.RequestIDs["x-request-id"] != "r1" || call.RequestIDs["request-id"] != "r2" || call.RequestIDs["apim-request-id"] != "a1" {
		t.Fatalf("%v", call.RequestIDs)
	}
}

type nopWriter struct{ http.ResponseWriter }

func (nopWriter) Header() http.Header         { return http.Header{} }
func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }
func (nopWriter) WriteHeader(int)             {}

type errWriteWriter struct{ nopWriter }

func (errWriteWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }
