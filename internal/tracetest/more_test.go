// SPDX-License-Identifier: AGPL-3.0-only

package tracetest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/barzoj-co/llmentary/internal/proxy"
)

func TestNewClientRejectsMissingSchemeAndHTTPClientNil(t *testing.T) {
	t.Parallel()
	if _, err := NewClient("127.0.0.1:9"); err == nil {
		t.Fatal("expected scheme/host error")
	}
	c, err := NewClient("http://127.0.0.1:9", WithHTTPClient(nil))
	if err != nil || c.httpClient != http.DefaultClient {
		t.Fatalf("nil http client %v %v", err, c)
	}
}

func TestClientOTLPAndResolveFallback(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/sessions/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "nope", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c, err := NewClient(srv.URL, WithHTTPClient(srv.Client()))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := c.OTLP(context.Background(), "50%")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil || got["ok"] != true {
		t.Fatalf("%s %v", raw, err)
	}
	prefixed, err := NewClient(srv.URL + "/prefix")
	if err != nil {
		t.Fatal(err)
	}
	u := prefixed.resolve("/sessions/x")
	if !strings.Contains(u.Path, "/prefix/sessions/x") {
		t.Fatalf("prefixed path=%q", u.Path)
	}
	u = c.resolve("http://example.com/sessions/x")
	if u == nil || u.Host == "" {
		t.Fatalf("resolve abs=%v", u)
	}
}

func TestSessionHTTPClientRoundTripAndFatals(t *testing.T) {
	var sawSession string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawSession = r.Header.Get(proxy.SessionHeaderName)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_ = json.NewEncoder(w).Encode(proxy.Session{ID: "x", Calls: []proxy.Call{{Seq: 1}}})
	}))
	t.Cleanup(control.Close)

	base := New(t, WithControlURL(control.URL), WithProxyURL(upstream.URL))
	base.httpClient = upstream.Client()
	res, err := base.HTTPClient().Get(upstream.URL + "/v1")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if sawSession != base.SessionID() {
		t.Fatalf("session header=%q want %q", sawSession, base.SessionID())
	}
}

func TestSessionWaitAndCallsFatal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "missing", http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	okTB := &fatalTB{TB: t}
	s := New(okTB, WithControlURL(srv.URL), WithWait(time.Millisecond, 10*time.Millisecond))
	if okTB.failed {
		t.Fatal("New should succeed")
	}
	failTB := &fatalTB{TB: t}
	s.t = failTB
	s.Wait(context.Background())
	if !failTB.failed {
		t.Fatal("wait should fatal")
	}
	failTB.failed = false
	s.Calls(context.Background())
	if !failTB.failed {
		t.Fatal("calls should fatal")
	}
}

type fatalTB struct {
	testing.TB
	failed bool
}

func (f *fatalTB) Helper() {}

func (f *fatalTB) Fatalf(format string, args ...any) {
	f.failed = true
}

func (f *fatalTB) Fatal(args ...any) {
	f.failed = true
}
