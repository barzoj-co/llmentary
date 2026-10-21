// SPDX-License-Identifier: AGPL-3.0-only

package tracetest

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/barzoj-co/llmentary/internal/proxy"
)

func TestSessionOptionsAndWait(t *testing.T) {
	var sawAuth, sawWait, sawDelete bool
	mux := http.NewServeMux()
	mux.HandleFunc("/sessions/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer tok" {
			sawAuth = true
		}
		switch {
		case r.Method == http.MethodDelete:
			sawDelete = true
			w.WriteHeader(http.StatusNoContent)
		case stringsHasSuffix(r.URL.Path, "/wait"):
			sawWait = true
			if r.URL.Query().Get("quiet") != "10ms" || r.URL.Query().Get("timeout") != "1s" {
				t.Errorf("wait query=%s", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode(proxy.Session{ID: "x", Calls: []proxy.Call{{Seq: 1, HTTPStatus: 200}}})
		case stringsHasSuffix(r.URL.Path, "/otlp"):
			_, _ = io.WriteString(w, `{"ok":true}`)
		default:
			_ = json.NewEncoder(w).Encode(proxy.Session{ID: "x", Calls: []proxy.Call{{Seq: 1, HTTPStatus: 200}}})
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	s := New(t,
		WithControlURL(srv.URL),
		WithProxyURL("http://proxy.test"),
		WithSessionControlToken("tok"),
		WithWait(10*time.Millisecond, time.Second),
	)
	if s.SessionID() == "" {
		t.Fatal("empty session id")
	}
	if s.ControlURL() != srv.URL {
		t.Fatalf("control=%s", s.ControlURL())
	}
	name, val := s.Header()
	if name != proxy.SessionHeaderName || val != s.SessionID() {
		t.Fatalf("header %s=%s", name, val)
	}
	if s.BaseURL("") != "http://proxy.test" {
		t.Fatalf("base=%s", s.BaseURL(""))
	}
	if s.OpenAIBaseURL("openai") != "http://proxy.test/u/openai" {
		t.Fatalf("openai base=%s", s.OpenAIBaseURL("openai"))
	}
	hc := s.HTTPClient()
	if hc == nil || hc.Transport == nil {
		t.Fatal("HTTPClient missing session transport")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	tr := s.Wait(ctx)
	if tr.SessionID != s.SessionID() || len(tr.Calls) != 1 {
		t.Fatalf("wait trace=%+v", tr)
	}
	tr = s.Calls(ctx)
	if len(tr.Calls) != 1 {
		t.Fatalf("calls=%+v", tr)
	}
	if !sawWait || !sawAuth {
		t.Fatalf("sawWait=%v sawAuth=%v", sawWait, sawAuth)
	}
	_ = sawDelete
}

func TestGetenvDefault(t *testing.T) {
	t.Setenv("LLMENTARY_PROXY_URL", "http://env-proxy")
	if getenvDefault("LLMENTARY_PROXY_URL", "fallback") != "http://env-proxy" {
		t.Fatal(getenvDefault("LLMENTARY_PROXY_URL", "fallback"))
	}
	if getenvDefault("LLMENTARY_NOT_SET_FOR_TEST", "fallback") != "fallback" {
		t.Fatal("fallback")
	}
}

func TestNewClientRejectsRelativeURL(t *testing.T) {
	if _, err := NewClient("/sessions"); err == nil {
		t.Fatal("expected error")
	}
}

func TestClientWaitAndDeleteErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			http.Error(w, "nope", http.StatusInternalServerError)
			return
		}
		http.Error(w, "missing", http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	c, err := NewClient(srv.URL, WithControlToken("t"), WithHTTPClient(srv.Client()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Wait(context.Background(), "s", time.Millisecond, time.Second); err == nil {
		t.Fatal("expected wait error")
	}
	if err := c.Delete(context.Background(), "s"); err == nil {
		t.Fatal("expected delete error")
	}
	if c.NewSession() == "" {
		t.Fatal("NewSession empty")
	}
}

func stringsHasSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}
