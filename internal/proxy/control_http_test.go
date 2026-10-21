// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestControlHealthzConfigVersionAndSessions(t *testing.T) {
	store := NewMemoryStore()
	s := newAbortTestServer(t, store)
	ts := httptest.NewServer(s.ControlHandler())
	t.Cleanup(ts.Close)

	res, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || string(body) != "ok\n" {
		t.Fatalf("healthz %d %q", res.StatusCode, body)
	}
	if !strings.Contains(res.Header.Get("Content-Type"), "text/plain") {
		t.Fatalf("ct=%s", res.Header.Get("Content-Type"))
	}

	res, err = http.Get(ts.URL + "/config")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("config %d", res.StatusCode)
	}

	res, err = http.Get(ts.URL + "/version")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	var ver map[string]string
	if err := json.Unmarshal(raw, &ver); err != nil || ver["version"] == "" {
		t.Fatalf("version %s err=%v", raw, err)
	}

	started := time.Now().UTC()
	_, id := store.Begin("s1", Call{StartedAt: started, SessionSource: "header"})
	store.Finish("s1", id, func(c *Call) { c.EndedAt = started })

	res, err = http.Get(ts.URL + "/sessions?source=header")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(raw), `"s1"`) {
		t.Fatalf("sessions header %d %s", res.StatusCode, raw)
	}

	res, err = http.Get(ts.URL + "/sessions?source=path")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || strings.Contains(string(raw), `"s1"`) {
		t.Fatalf("sessions path filter %s", raw)
	}

	res, err = http.Get(ts.URL + "/sessions?source=nope")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad source %d", res.StatusCode)
	}
}

func TestControlRejectsUnknownPathQueryAndMethod(t *testing.T) {
	s := newAbortTestServer(t, NewMemoryStore())
	ts := httptest.NewServer(s.ControlHandler())
	t.Cleanup(ts.Close)

	res, err := http.Get(ts.URL + "/nope")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNotFound || !strings.Contains(string(raw), "openapi.yaml") {
		t.Fatalf("unknown path %d %s", res.StatusCode, raw)
	}

	res, err = http.Get(ts.URL + "/healthz?foo=1")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown query %d", res.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/healthz", nil)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusMethodNotAllowed || res.Header.Get("Allow") != "GET" {
		t.Fatalf("method %d allow=%s", res.StatusCode, res.Header.Get("Allow"))
	}

	res, err = http.Get(ts.URL + "/sessions/x/wait?bogus=1")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("wait query %d", res.StatusCode)
	}

	res, err = http.Get(ts.URL + "/sessions/x/wait?quiet=abc")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad quiet %d", res.StatusCode)
	}

	res, err = http.Get(ts.URL + "/sessions/x/wait?timeout=11m")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad timeout %d", res.StatusCode)
	}

	res, err = http.Get(ts.URL + "/sessions/x/wait?min_calls=-1")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad min_calls %d", res.StatusCode)
	}

	res, err = http.Get(ts.URL + "/sessions/%2F")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNotFound && res.StatusCode != http.StatusBadRequest {
		t.Fatalf("slash id %d", res.StatusCode)
	}
}

func TestControlAuthAndOpenAPI(t *testing.T) {
	cfg := Config{
		ListenAddr:     ":0",
		ControlAddr:    "127.0.0.1:0",
		ControlToken:   "secret",
		SpecValidation: "off",
		SpecProfile:    "relaxed",
	}
	s, err := NewServer(cfg, NewMemoryStore(), TolerantParser{})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.ControlHandler())
	t.Cleanup(ts.Close)

	res, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauth %d", res.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/healthz", nil)
	req.Header.Set("Authorization", "Bearer secret")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || string(body) != "ok\n" {
		t.Fatalf("authed healthz %d %q", res.StatusCode, body)
	}

	req, _ = http.NewRequest(http.MethodGet, ts.URL+"/openapi.yaml", nil)
	req.Header.Set("Authorization", "Bearer secret")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(raw), "openapi:") {
		t.Fatalf("openapi %d %s", res.StatusCode, raw[:min(80, len(raw))])
	}
}

func TestParseMinCallsAndValidSessionSource(t *testing.T) {
	t.Parallel()
	n, err := parseMinCalls("")
	if err != nil || n != 0 {
		t.Fatalf("empty %d %v", n, err)
	}
	n, err = parseMinCalls("3")
	if err != nil || n != 3 {
		t.Fatalf("3 %d %v", n, err)
	}
	if _, err := parseMinCalls("x"); err == nil {
		t.Fatal("x")
	}
	if _, err := parseMinCalls("-2"); err == nil {
		t.Fatal("neg")
	}
	for _, src := range []string{"default", "header", "baggage", "path"} {
		if !validSessionSource(src) {
			t.Fatalf("%s", src)
		}
	}
	if validSessionSource("cookie") {
		t.Fatal("cookie")
	}
}

func TestWriteWaitTimeoutEmptyCalls(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	writeWaitTimeout(rec, Session{ID: "s"}, 2)
	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("code=%d", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	calls, ok := body["calls"].([]any)
	if !ok || calls == nil {
		t.Fatalf("calls=%v", body["calls"])
	}
}
