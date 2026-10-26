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

func TestWaitTimeoutSnapshotAndStrippedQuery(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	s := newAbortTestServer(t, store)
	ts := httptest.NewServer(s.ControlHandler())
	t.Cleanup(ts.Close)

	now := time.Now().UTC()
	_, settledID := store.Begin("s", Call{StartedAt: now, Upstream: Upstream{URL: "https://api.example/v1/a"}})
	store.Finish("s", settledID, func(call *Call) {
		call.EndedAt = now
		call.HTTPStatus = 200
	})
	store.Begin("s", Call{StartedAt: now, Upstream: Upstream{URL: "https://api.example/v1/b?access_token=SECRET&keep=1"}})

	res, err := http.Get(ts.URL + "/sessions/s/wait?timeout=1ms&quiet=50ms&min_calls=2")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("status=%d body=%s", res.StatusCode, raw)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	calls, _ := body["calls"].([]any)
	errObj, _ := body["error"].(map[string]any)
	inFlight, _ := errObj["in_flight"].([]any)
	if len(calls) != 1 || len(inFlight) != 1 {
		t.Fatalf("calls=%v in_flight=%v", body["calls"], errObj["in_flight"])
	}
	item, _ := inFlight[0].(map[string]any)
	upstream, _ := item["upstream"].(string)
	if upstream != "https://api.example/v1/b" {
		t.Fatalf("wait in-flight upstream=%q", upstream)
	}

	res, err = http.Get(ts.URL + "/sessions/s?include=in_flight")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	body = map[string]any{}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	calls, _ = body["calls"].([]any)
	inFlight, _ = body["in_flight"].([]any)
	if len(calls) != 1 || len(inFlight) != 1 {
		t.Fatalf("include calls=%v in_flight=%v", body["calls"], body["in_flight"])
	}
	item, _ = inFlight[0].(map[string]any)
	upstream, _ = item["upstream"].(string)
	if upstream != "https://api.example/v1/b" {
		t.Fatalf("include in-flight upstream=%q", upstream)
	}
}

func TestWaitExcludeCancelledParam(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	s := newAbortTestServer(t, store)
	ts := httptest.NewServer(s.ControlHandler())
	t.Cleanup(ts.Close)

	getWait := func(query string) (int, map[string]any, []byte) {
		t.Helper()
		res, err := http.Get(ts.URL + "/sessions/s/wait?" + query)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("status=%d body=%s err=%v", res.StatusCode, raw, err)
		}
		return res.StatusCode, body, raw
	}

	now := time.Now().UTC()
	_, settledID := store.Begin("s", Call{StartedAt: now})
	store.Finish("s", settledID, func(call *Call) {
		call.EndedAt = time.Now().UTC()
		call.HTTPStatus = 200
	})

	status, body, raw := getWait("exclude_cancelled=yes&quiet=20ms&timeout=2s")
	if status != http.StatusBadRequest {
		t.Fatalf("bad exclude_cancelled status=%d body=%s", status, raw)
	}
	errObj, _ := body["error"].(map[string]any)
	if errObj["type"] != "invalid_parameter" || errObj["param"] != "exclude_cancelled" {
		t.Fatalf("bad exclude_cancelled error=%v", body["error"])
	}

	status, _, raw = getWait("exclude_cancelled=true&quiet=20ms&timeout=2s&min_calls=1")
	if status != http.StatusOK {
		t.Fatalf("accepted status=%d body=%s", status, raw)
	}

	status, body, raw = getWait("exclude_cancelled=true&quiet=20ms&timeout=100ms&min_calls=2")
	if status != http.StatusGatewayTimeout {
		t.Fatalf("timeout status=%d body=%s", status, raw)
	}
	errObj, _ = body["error"].(map[string]any)
	if errObj["exclude_cancelled"] != true {
		t.Fatalf("timeout error=%v want exclude_cancelled:true", body["error"])
	}

	status, body, raw = getWait("quiet=20ms&timeout=100ms&min_calls=2")
	if status != http.StatusGatewayTimeout {
		t.Fatalf("default status=%d body=%s", status, raw)
	}
	errObj, _ = body["error"].(map[string]any)
	if _, ok := errObj["exclude_cancelled"]; ok {
		t.Fatalf("default timeout error=%v must not echo exclude_cancelled", body["error"])
	}
}

func TestWriteWaitTimeoutEmptyCalls(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	writeWaitTimeout(rec, Session{ID: "s"}, 2, nil, false)
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
	errObj, _ := body["error"].(map[string]any)
	if intFromAny(errObj["active"]) != 0 {
		t.Fatalf("active=%v", errObj["active"])
	}
	inFlight, ok := errObj["in_flight"].([]any)
	if !ok || inFlight == nil {
		t.Fatalf("in_flight=%v", errObj["in_flight"])
	}
}

func TestWriteSessionNotFoundWait(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	writeSessionNotFoundWait(rec, "ghost", 12)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "session_not_found") || !strings.Contains(body, "ghost") || !strings.Contains(body, `"waited_ms":12`) {
		t.Fatalf("body=%s", body)
	}
}

func intFromAny(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	default:
		return -1
	}
}
