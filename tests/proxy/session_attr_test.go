// SPDX-License-Identifier: AGPL-3.0-only

package proxy_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/barzoj-co/llmentary/internal/proxy"
	"github.com/barzoj-co/llmentary/internal/tracetest"
)

type echoUpstream struct {
	mu      sync.Mutex
	hits    int
	path    string
	query   string
	headers http.Header
	body    []byte
	handler http.HandlerFunc
}

func (u *echoUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	u.mu.Lock()
	u.hits++
	u.path = r.URL.Path
	u.query = r.URL.RawQuery
	u.headers = r.Header.Clone()
	u.body = append([]byte(nil), body...)
	h := u.handler
	u.mu.Unlock()
	if h != nil {
		h(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"id":"c","model":"gpt-fixture","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
}

func (u *echoUpstream) snapshot() (hits int, path, query string, headers http.Header, body []byte) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.hits, u.path, u.query, u.headers.Clone(), append([]byte(nil), u.body...)
}

func startPathProxy(t *testing.T, echo *echoUpstream, extra ...func(*proxy.Config)) (proxyURL, controlURL string) {
	t.Helper()
	fake := httptest.NewServer(echo)
	t.Cleanup(fake.Close)
	cfg := proxy.Config{ListenAddr: ":0", ControlAddr: "127.0.0.1:0"}
	if err := cfg.AddUpstream("openai=" + fake.URL); err != nil {
		t.Fatal(err)
	}
	for _, fn := range extra {
		fn(&cfg)
	}
	server, err := proxy.NewServer(cfg, proxy.NewMemoryStore(), proxy.TolerantParser{})
	if err != nil {
		t.Fatal(err)
	}
	proxySrv := httptest.NewServer(server.ProxyHandler())
	t.Cleanup(proxySrv.Close)
	control := httptest.NewServer(server.ControlHandler())
	t.Cleanup(control.Close)
	return proxySrv.URL, control.URL
}

func waitCalls(t *testing.T, controlURL, session string) []proxy.Call {
	t.Helper()
	client, err := tracetest.NewClient(controlURL)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	calls, err := client.Wait(ctx, session, 10*time.Millisecond, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return calls
}

func postJSON(t *testing.T, url string, headers map[string]string, body []byte) (int, http.Header, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client := &http.Client{Transport: &http.Transport{DisableCompression: true}}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, res.Header.Clone(), raw
}

func openaiErrorType(t *testing.T, body []byte) string {
	t.Helper()
	var env struct {
		Error struct {
			Type    string `json:"type"`
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("json: %v body=%s", err, body)
	}
	return env.Error.Type
}

func TestA1PathPrefixChatAndResponses(t *testing.T) {
	chatBody := []byte(`{"model":"gpt-fixture","messages":[{"role":"user","content":"hi"}]}`)
	streamBody := []byte(`{"model":"gpt-fixture","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	respBody := []byte(`{"model":"gpt-fixture","input":"hi"}`)
	sse := []byte("data: {\"id\":\"chatcmpl_1\",\"model\":\"gpt-fixture\",\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	cases := []struct {
		name, path, contentType string
		req, up                 []byte
	}{
		{name: "chat_nonstream", path: "/s/t1/v1/chat/completions", contentType: "application/json", req: chatBody, up: []byte(`{"id":"c","model":"gpt-fixture","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)},
		{name: "chat_stream", path: "/s/t1/v1/chat/completions", contentType: "text/event-stream", req: streamBody, up: sse},
		{name: "responses_nonstream", path: "/s/t1/v1/responses", contentType: "application/json", req: respBody, up: []byte(`{"id":"r","model":"gpt-fixture","status":"completed","output":[]}`)},
		{name: "responses_stream", path: "/s/t1/v1/responses", contentType: "text/event-stream", req: []byte(`{"model":"gpt-fixture","input":"hi","stream":true}`), up: []byte("event: response.completed\ndata: {\"type\":\"response.completed\"}\n\n")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			echo := &echoUpstream{handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				_, _ = w.Write(tc.up)
			}}
			proxyURL, controlURL := startPathProxy(t, echo)
			status, _, _ := postJSON(t, proxyURL+tc.path, nil, tc.req)
			if status != http.StatusOK {
				t.Fatalf("status=%d", status)
			}
			hits, path, _, _, _ := echo.snapshot()
			if hits != 1 {
				t.Fatalf("hits=%d", hits)
			}
			if path != "/v1/chat/completions" && path != "/v1/responses" {
				t.Fatalf("upstream path=%q", path)
			}
			calls := waitCalls(t, controlURL, "t1")
			if len(calls) != 1 {
				t.Fatalf("calls=%d", len(calls))
			}
			if calls[0].SessionID != "t1" || calls[0].SessionSource != "path" {
				t.Fatalf("session=%s source=%s", calls[0].SessionID, calls[0].SessionSource)
			}
		})
	}
}

func TestA2PathPrefixNamedUpstream(t *testing.T) {
	echo := &echoUpstream{}
	fake := httptest.NewServer(echo)
	t.Cleanup(fake.Close)
	cfg := proxy.Config{ListenAddr: ":0", ControlAddr: "127.0.0.1:0"}
	if err := cfg.AddUpstream("openrouter=" + fake.URL + "/api/v1"); err != nil {
		t.Fatal(err)
	}
	server, err := proxy.NewServer(cfg, proxy.NewMemoryStore(), proxy.TolerantParser{})
	if err != nil {
		t.Fatal(err)
	}
	proxySrv := httptest.NewServer(server.ProxyHandler())
	t.Cleanup(proxySrv.Close)
	control := httptest.NewServer(server.ControlHandler())
	t.Cleanup(control.Close)
	status, _, _ := postJSON(t, proxySrv.URL+"/s/t1/u/openrouter/chat/completions", nil, []byte(`{"model":"gpt-fixture","messages":[{"role":"user","content":"hi"}]}`))
	if status != http.StatusOK {
		t.Fatalf("status=%d", status)
	}
	hits, path, _, _, _ := echo.snapshot()
	if hits != 1 || path != "/api/v1/chat/completions" {
		t.Fatalf("hits=%d path=%q", hits, path)
	}
	calls := waitCalls(t, control.URL, "t1")
	if len(calls) != 1 || calls[0].SessionID != "t1" || calls[0].SessionSource != "path" {
		t.Fatalf("%+v", calls)
	}
}

func TestA3PercentEncodedAndEmptySession(t *testing.T) {
	t.Run("percent", func(t *testing.T) {
		echo := &echoUpstream{}
		proxyURL, controlURL := startPathProxy(t, echo)
		status, _, _ := postJSON(t, proxyURL+"/s/50%25/v1/chat/completions", nil, []byte(`{"model":"gpt-fixture"}`))
		if status != http.StatusOK {
			t.Fatalf("status=%d", status)
		}
		calls := waitCalls(t, controlURL, "50%")
		if len(calls) != 1 || calls[0].SessionID != "50%" || calls[0].SessionSource != "path" {
			t.Fatalf("%+v", calls)
		}
	})
	t.Run("space", func(t *testing.T) {
		echo := &echoUpstream{}
		proxyURL, controlURL := startPathProxy(t, echo)
		status, _, _ := postJSON(t, proxyURL+"/s/test%20one/v1/chat/completions", nil, []byte(`{"model":"gpt-fixture"}`))
		if status != http.StatusOK {
			t.Fatalf("status=%d", status)
		}
		calls := waitCalls(t, controlURL, "test one")
		if len(calls) != 1 || calls[0].SessionID != "test one" || calls[0].SessionSource != "path" {
			t.Fatalf("%+v", calls)
		}
	})
	t.Run("empty", func(t *testing.T) {
		echo := &echoUpstream{}
		proxyURL, _ := startPathProxy(t, echo)
		status, _, body := postJSON(t, proxyURL+"/s//v1/chat/completions", nil, []byte(`{}`))
		if status != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", status, body)
		}
		if hits, _, _, _, _ := echo.snapshot(); hits != 0 {
			t.Fatalf("hits=%d", hits)
		}
	})
	t.Run("encoded_slash", func(t *testing.T) {
		echo := &echoUpstream{}
		proxyURL, _ := startPathProxy(t, echo)
		status, _, body := postJSON(t, proxyURL+"/s/%2F/v1/chat/completions", nil, []byte(`{}`))
		if status != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", status, body)
		}
		if hits, _, _, _, _ := echo.snapshot(); hits != 0 {
			t.Fatalf("hits=%d", hits)
		}
	})
}

func TestA4SessionPrecedence(t *testing.T) {
	t.Run("default_header_wins", func(t *testing.T) {
		echo := &echoUpstream{}
		proxyURL, controlURL := startPathProxy(t, echo)
		status, _, _ := postJSON(t, proxyURL+"/s/p/v1/chat/completions", map[string]string{proxy.SessionHeaderName: "h"}, []byte(`{"model":"gpt-fixture"}`))
		if status != http.StatusOK {
			t.Fatalf("status=%d", status)
		}
		calls := waitCalls(t, controlURL, "h")
		if len(calls) != 1 || calls[0].SessionID != "h" || calls[0].SessionSource != "header" {
			t.Fatalf("%+v", calls)
		}
	})
	t.Run("path_then_header", func(t *testing.T) {
		echo := &echoUpstream{}
		proxyURL, controlURL := startPathProxy(t, echo, func(cfg *proxy.Config) {
			cfg.SessionFrom = []string{"path", "header"}
		})
		status, _, _ := postJSON(t, proxyURL+"/s/p/v1/chat/completions", map[string]string{proxy.SessionHeaderName: "h"}, []byte(`{"model":"gpt-fixture"}`))
		if status != http.StatusOK {
			t.Fatalf("status=%d", status)
		}
		calls := waitCalls(t, controlURL, "p")
		if len(calls) != 1 || calls[0].SessionID != "p" || calls[0].SessionSource != "path" {
			t.Fatalf("%+v", calls)
		}
	})
}

func TestB1DefaultSession(t *testing.T) {
	echo := &echoUpstream{}
	proxyURL, controlURL := startPathProxy(t, echo, func(cfg *proxy.Config) {
		cfg.DefaultSession = "svc"
	})
	status, _, _ := postJSON(t, proxyURL+"/v1/chat/completions", nil, []byte(`{"model":"gpt-fixture"}`))
	if status != http.StatusOK {
		t.Fatalf("status=%d", status)
	}
	calls := waitCalls(t, controlURL, "svc")
	if len(calls) != 1 || calls[0].SessionID != "svc" || calls[0].SessionSource != "default" {
		t.Fatalf("%+v", calls)
	}
}

func TestB2SessionFromPathRejectsUntagged(t *testing.T) {
	echo := &echoUpstream{}
	proxyURL, controlURL := startPathProxy(t, echo, func(cfg *proxy.Config) {
		cfg.SessionFrom = []string{"path"}
	})
	status, _, body := postJSON(t, proxyURL+"/v1/chat/completions", nil, []byte(`{"model":"gpt-fixture"}`))
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", status, body)
	}
	if openaiErrorType(t, body) != "llmentary_session_required" {
		t.Fatalf("body=%s", body)
	}
	if hits, _, _, _, _ := echo.snapshot(); hits != 0 {
		t.Fatalf("hits=%d", hits)
	}
	status, _, _ = postJSON(t, proxyURL+"/s/t1/v1/chat/completions", nil, []byte(`{"model":"gpt-fixture"}`))
	if status != http.StatusOK {
		t.Fatalf("path-tagged status=%d", status)
	}
	calls := waitCalls(t, controlURL, "t1")
	if len(calls) != 1 || calls[0].SessionSource != "path" {
		t.Fatalf("%+v", calls)
	}
}

func TestB3InvalidSessionFromExits(t *testing.T) {
	bin := proxyBinary(t)
	for _, raw := range []string{"foo", ""} {
		cmd := exec.Command(bin, "--session-from="+raw, "--upstream", "https://example.com")
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("%q: expected non-zero exit; out=%s", raw, out)
		}
		if !bytes.Contains(out, []byte("--session-from")) || !bytes.Contains(out, []byte("header, baggage, path, default")) {
			t.Fatalf("%q message=%s", raw, out)
		}
	}
}

func TestC1UnknownPrefixNotForwarded(t *testing.T) {
	var logBuf bytes.Buffer
	echo := &echoUpstream{}
	proxyURL, _ := startPathProxy(t, echo, func(cfg *proxy.Config) {
		cfg.Logger = slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	})
	status, _, body := postJSON(t, proxyURL+"/dogfood/v1/chat/completions", map[string]string{
		"Authorization": "Bearer secret-must-not-appear",
	}, []byte(`{"model":"gpt-fixture"}`))
	if status != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", status, body)
	}
	if openaiErrorType(t, body) != "llmentary_unknown_path" {
		t.Fatalf("body=%s", body)
	}
	if !bytes.Contains(body, []byte("did you mean /s/dogfood/v1/")) {
		t.Fatalf("hint missing: %s", body)
	}
	if hits, _, _, _, _ := echo.snapshot(); hits != 0 {
		t.Fatalf("hits=%d", hits)
	}
	log := logBuf.String()
	if strings.Count(log, "unknown path") != 1 {
		t.Fatalf("want exactly one WARN, log=%s", log)
	}
	if strings.Contains(log, "secret-must-not-appear") || strings.Contains(log, "Authorization") || strings.Contains(log, "Bearer") {
		t.Fatalf("WARN leaked header values: %s", log)
	}
}

func TestC2AllowlistedFirstSegments(t *testing.T) {
	echo := &echoUpstream{}
	proxyURL, _ := startPathProxy(t, echo, func(cfg *proxy.Config) {
		cfg.AllowPathPrefixes = []string{"custom"}
	})
	prefixes := []string{
		"v1", "openai", "api", "chat", "responses", "completions",
		"embeddings", "models", "files", "audio", "images",
		"moderations", "batches", "uploads", "vector_stores",
		"assistants", "threads", "realtime", "custom",
	}
	for _, seg := range prefixes {
		status, _, body := postJSON(t, proxyURL+"/"+seg+"/x", nil, []byte(`{"ok":true}`))
		if status != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", seg, status, body)
		}
	}
	hits, _, _, _, _ := echo.snapshot()
	if hits != len(prefixes) {
		t.Fatalf("hits=%d want %d", hits, len(prefixes))
	}
}

func TestHopByHopConnectionNamedNotForwarded(t *testing.T) {
	echo := &echoUpstream{}
	proxyURL, _ := startPathProxy(t, echo)
	status, _, _ := postJSON(t, proxyURL+"/v1/chat/completions", map[string]string{
		"Connection":    "X-Internal",
		"X-Internal":    "secret",
		"Authorization": "Bearer test-token",
	}, []byte(`{"model":"gpt-fixture"}`))
	if status != http.StatusOK {
		t.Fatalf("status=%d", status)
	}
	_, _, _, headers, _ := echo.snapshot()
	if headers.Get("X-Internal") != "" || headers.Get("Connection") != "" {
		t.Fatalf("Connection-named hop-by-hop leaked: %v", headers)
	}
	if headers.Get("Authorization") != "Bearer test-token" {
		t.Fatalf("Authorization=%q", headers.Get("Authorization"))
	}
}

func TestD1EchoHeadersByteIdentical(t *testing.T) {
	echo := &echoUpstream{}
	proxyURL, _ := startPathProxy(t, echo)
	body := []byte(`{"model":"gpt-fixture","messages":[{"role":"user","content":"hi"}]}`)
	status, _, _ := postJSON(t, proxyURL+"/v1/chat/completions?keep=yes", map[string]string{
		"Authorization":         "Bearer test-token",
		"OpenAI-Organization":   "org-fixture",
		"X-Custom-Foo":          "bar",
		"Accept-Encoding":       "gzip",
		proxy.SessionHeaderName: "t1",
		"baggage":               "llmentary.session.id=from-baggage, other=value",
	}, body)
	if status != http.StatusOK {
		t.Fatalf("status=%d", status)
	}
	hits, _, query, headers, gotBody := echo.snapshot()
	if hits != 1 {
		t.Fatalf("hits=%d", hits)
	}
	if query != "keep=yes" {
		t.Fatalf("query=%q", query)
	}
	if !bytes.Equal(gotBody, body) {
		t.Fatalf("body mutated: %q", gotBody)
	}
	if headers.Get("Authorization") != "Bearer test-token" {
		t.Fatalf("Authorization=%q", headers.Get("Authorization"))
	}
	if headers.Get("OpenAI-Organization") != "org-fixture" {
		t.Fatalf("OpenAI-Organization=%q", headers.Get("OpenAI-Organization"))
	}
	if headers.Get("X-Custom-Foo") != "bar" {
		t.Fatalf("X-Custom-Foo=%q", headers.Get("X-Custom-Foo"))
	}
	if headers.Get("Accept-Encoding") != "gzip" {
		t.Fatalf("Accept-Encoding=%q (must not force identity)", headers.Get("Accept-Encoding"))
	}
	if headers.Get(proxy.SessionHeaderName) != "" {
		t.Fatal("session header leaked")
	}
	if headers.Get("baggage") != "other=value" {
		t.Fatalf("baggage=%q", headers.Get("baggage"))
	}
	if strings.Contains(strings.ToLower(headers.Get("baggage")), "llmentary.") {
		t.Fatalf("llmentary baggage leaked: %q", headers.Get("baggage"))
	}
}

func TestD2PropagateTraceOffAndAuto(t *testing.T) {
	const clientTP = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-00"
	t.Run("off_forwards_client", func(t *testing.T) {
		echo := &echoUpstream{}
		proxyURL, _ := startPathProxy(t, echo)
		_, _, _ = postJSON(t, proxyURL+"/v1/chat/completions", map[string]string{"traceparent": clientTP}, []byte(`{"model":"gpt-fixture"}`))
		_, _, _, headers, _ := echo.snapshot()
		if headers.Get("traceparent") != clientTP {
			t.Fatalf("traceparent=%q", headers.Get("traceparent"))
		}
	})
	t.Run("off_adds_none", func(t *testing.T) {
		echo := &echoUpstream{}
		proxyURL, _ := startPathProxy(t, echo)
		_, _, _ = postJSON(t, proxyURL+"/v1/chat/completions", nil, []byte(`{"model":"gpt-fixture"}`))
		_, _, _, headers, _ := echo.snapshot()
		if headers.Get("traceparent") != "" {
			t.Fatalf("default off must not inject traceparent: %q", headers.Get("traceparent"))
		}
	})
	t.Run("auto_injects", func(t *testing.T) {
		echo := &echoUpstream{}
		proxyURL, _ := startPathProxy(t, echo, func(cfg *proxy.Config) {
			cfg.PropagateTrace = proxy.PropagateTraceAuto
		})
		_, _, _ = postJSON(t, proxyURL+"/v1/chat/completions", nil, []byte(`{"model":"gpt-fixture"}`))
		_, _, _, headers, _ := echo.snapshot()
		if headers.Get("traceparent") == "" {
			t.Fatal("auto must inject traceparent when absent")
		}
	})
	t.Run("auto_continues", func(t *testing.T) {
		echo := &echoUpstream{}
		proxyURL, _ := startPathProxy(t, echo, func(cfg *proxy.Config) {
			cfg.PropagateTrace = proxy.PropagateTraceAuto
		})
		_, _, _ = postJSON(t, proxyURL+"/v1/chat/completions", map[string]string{"traceparent": clientTP}, []byte(`{"model":"gpt-fixture"}`))
		_, _, _, headers, _ := echo.snapshot()
		got := headers.Get("traceparent")
		if got == "" || !strings.Contains(got, "4bf92f3577b34da6a3ce929d0e0e4736") {
			t.Fatalf("auto should continue trace id: %q", got)
		}
	})
}

func TestD2InvalidPropagateTraceExits(t *testing.T) {
	cmd := exec.Command(proxyBinary(t), "--propagate-trace=always", "--upstream", "https://example.com")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit; out=%s", out)
	}
	if !bytes.Contains(out, []byte("--propagate-trace")) || !bytes.Contains(out, []byte("off, auto")) {
		t.Fatalf("message=%s", out)
	}
}

func TestD3DocsListEveryMutation(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "proxy.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	for _, name := range []string{
		"X-Test-Session",
		"llmentary.*",
		"/s/{session}",
		"/u/{name}",
		"hop-by-hop",
		"Host",
		"traceparent",
		"Accept-Encoding",
	} {
		if !strings.Contains(doc, name) {
			t.Fatalf("docs/proxy.md missing mutation name %q", name)
		}
	}
}

func TestGzipClientBytesUnchangedAndRecordedDecompressed(t *testing.T) {
	plain := []byte(`{"id":"c","model":"gpt-fixture","choices":[{"message":{"role":"assistant","content":"gz"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write(plain)
	_ = zw.Close()
	echo := &echoUpstream{handler: func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(gz.Bytes())
	}}
	proxyURL, controlURL := startPathProxy(t, echo)
	status, hdr, clientBody := postJSON(t, proxyURL+"/v1/chat/completions", map[string]string{
		"Accept-Encoding":       "gzip",
		proxy.SessionHeaderName: "gz1",
	}, []byte(`{"model":"gpt-fixture"}`))
	if status != http.StatusOK {
		t.Fatalf("status=%d", status)
	}
	if !strings.EqualFold(hdr.Get("Content-Encoding"), "gzip") {
		t.Fatalf("client Content-Encoding=%q", hdr.Get("Content-Encoding"))
	}
	if !bytes.Equal(clientBody, gz.Bytes()) {
		t.Fatal("client must receive gzip bytes unchanged")
	}
	_, _, _, upHeaders, _ := echo.snapshot()
	if upHeaders.Get("Accept-Encoding") != "gzip" {
		t.Fatalf("upstream Accept-Encoding=%q", upHeaders.Get("Accept-Encoding"))
	}
	calls := waitCalls(t, controlURL, "gz1")
	if len(calls) != 1 {
		t.Fatalf("calls=%d", len(calls))
	}
	if !bytes.Equal(calls[0].RawResponse, plain) {
		t.Fatalf("recorded body must be decompressed, got %q", calls[0].RawResponse)
	}
}
