// SPDX-License-Identifier: AGPL-3.0-only

package proxy_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/barzoj-co/llmentary/internal/proxy"
)

func startControl(t *testing.T) (proxyURL, controlURL string, server *proxy.Server) {
	t.Helper()
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"chatcmpl_1","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	t.Cleanup(fake.Close)
	cfg := proxy.Config{ListenAddr: ":0", ControlAddr: "127.0.0.1:0"}
	if err := cfg.AddUpstream("default=" + fake.URL); err != nil {
		t.Fatal(err)
	}
	var err error
	server, err = proxy.NewServer(cfg, proxy.NewMemoryStore(), proxy.TolerantParser{})
	if err != nil {
		t.Fatal(err)
	}
	proxySrv := httptest.NewServer(server.ProxyHandler())
	t.Cleanup(proxySrv.Close)
	control := httptest.NewServer(server.ControlHandler())
	t.Cleanup(control.Close)
	return proxySrv.URL, control.URL, server
}

func getJSON(t *testing.T, url string) (int, http.Header, map[string]any) {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("json %s: %v body=%s", url, err, raw)
		}
	}
	return res.StatusCode, res.Header.Clone(), body
}

func postCall(t *testing.T, proxyURL, session string) {
	t.Helper()
	if err := doPostCall(proxyURL, session); err != nil {
		t.Fatal(err)
	}
}

func postCallErr(t *testing.T, proxyURL, session string) {
	t.Helper()
	if err := doPostCall(proxyURL, session); err != nil {
		t.Errorf("%v", err)
	}
}

func doPostCall(proxyURL, session string) error {
	req, err := http.NewRequest(http.MethodPost, proxyURL+"/v1/chat/completions", strings.NewReader(`{"model":"gpt-fixture","messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(proxy.SessionHeaderName, session)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	_, _ = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("proxy status=%d", res.StatusCode)
	}
	return nil
}

func TestP1SessionList(t *testing.T) {
	_, control, _ := startControl(t)
	for _, path := range []string{"/sessions", "/sessions/"} {
		status, hdr, body := getJSON(t, control+path)
		if status != http.StatusOK {
			t.Fatalf("%s status=%d", path, status)
		}
		if !strings.Contains(hdr.Get("Content-Type"), "application/json") {
			t.Fatalf("%s content-type=%q", path, hdr.Get("Content-Type"))
		}
		sessions, ok := body["sessions"].([]any)
		if !ok || sessions == nil {
			t.Fatalf("%s body=%v", path, body)
		}
		if len(sessions) != 0 {
			t.Fatalf("%s want empty list, got %#v", path, sessions)
		}
	}
}

func TestP2UnknownSessionNoSideEffect(t *testing.T) {
	_, control, _ := startControl(t)
	for _, path := range []string{"/sessions/nope", "/sessions/nope/otlp"} {
		status, hdr, body := getJSON(t, control+path)
		if status != http.StatusNotFound {
			t.Fatalf("%s status=%d body=%v", path, status, body)
		}
		if !strings.Contains(hdr.Get("Content-Type"), "application/json") {
			t.Fatalf("content-type=%q", hdr.Get("Content-Type"))
		}
		errObj, _ := body["error"].(map[string]any)
		if fmtString(errObj["type"]) != "session_not_found" {
			t.Fatalf("error=%v", body["error"])
		}
	}
	_, _, listed := getJSON(t, control+"/sessions")
	sessions, _ := listed["sessions"].([]any)
	if len(sessions) != 0 {
		t.Fatalf("GET created a session: %#v", sessions)
	}
}

func TestP3DeleteUnknownAndKnown(t *testing.T) {
	proxyURL, control, _ := startControl(t)
	req, err := http.NewRequest(http.MethodDelete, control+"/sessions/missing", nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("delete unknown status=%d", res.StatusCode)
	}
	postCall(t, proxyURL, "keep")
	status, _, _ := getJSON(t, control+"/sessions/keep")
	if status != http.StatusOK {
		t.Fatalf("known session status=%d", status)
	}
	del, err := http.NewRequest(http.MethodDelete, control+"/sessions/keep", nil)
	if err != nil {
		t.Fatal(err)
	}
	dres, err := http.DefaultClient.Do(del)
	if err != nil {
		t.Fatal(err)
	}
	_ = dres.Body.Close()
	if dres.StatusCode != http.StatusNoContent {
		t.Fatalf("delete known status=%d", dres.StatusCode)
	}
	status, _, body := getJSON(t, control+"/sessions/keep")
	if status != http.StatusNotFound {
		t.Fatalf("after delete status=%d body=%v", status, body)
	}
}

func TestP4WaitMinCalls(t *testing.T) {
	proxyURL, control, _ := startControl(t)
	done := make(chan struct{})
	go func() {
		defer close(done)
		postCallErr(t, proxyURL, "async")
		time.Sleep(time.Second)
		postCallErr(t, proxyURL, "async")
	}()
	t.Cleanup(func() { <-done })
	started := time.Now()
	status, _, body := getJSON(t, control+"/sessions/async/wait?min_calls=2&quiet=100ms&timeout=5s")
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%v", status, body)
	}
	calls, _ := body["calls"].([]any)
	if len(calls) != 2 {
		t.Fatalf("calls=%d body=%v", len(calls), body)
	}
	if time.Since(started) < time.Second {
		t.Fatalf("returned too early: %s", time.Since(started))
	}
}

func TestP5WaitUnknownAppearsOrTimeout(t *testing.T) {
	proxyURL, control, _ := startControl(t)
	t.Run("arrives", func(t *testing.T) {
		done := make(chan struct{})
		go func() {
			defer close(done)
			time.Sleep(time.Second)
			postCallErr(t, proxyURL, "late")
		}()
		t.Cleanup(func() { <-done })
		status, _, body := getJSON(t, control+"/sessions/late/wait?min_calls=1&quiet=50ms&timeout=5s")
		if status != http.StatusOK {
			t.Fatalf("status=%d body=%v", status, body)
		}
		calls, _ := body["calls"].([]any)
		if len(calls) != 1 {
			t.Fatalf("calls=%d", len(calls))
		}
	})
	t.Run("never", func(t *testing.T) {
		started := time.Now()
		status, hdr, body := getJSON(t, control+"/sessions/never/wait?quiet=50ms&timeout=200ms")
		if status != http.StatusGatewayTimeout {
			t.Fatalf("status=%d body=%v", status, body)
		}
		if !strings.Contains(hdr.Get("Content-Type"), "application/json") {
			t.Fatalf("content-type=%q", hdr.Get("Content-Type"))
		}
		errObj, _ := body["error"].(map[string]any)
		if fmtString(errObj["type"]) != "wait_timeout" {
			t.Fatalf("error=%v", body["error"])
		}
		if intFrom(errObj["calls_seen"]) != 0 {
			t.Fatalf("calls_seen=%v", errObj["calls_seen"])
		}
		if time.Since(started) > 400*time.Millisecond {
			t.Fatalf("timeout too slow: %s", time.Since(started))
		}
	})
}

func TestWaitQuietSecondCallImmediate(t *testing.T) {
	proxyURL, control, _ := startControl(t)
	postCall(t, proxyURL, "idle")
	status, _, body := getJSON(t, control+"/sessions/idle/wait?quiet=500ms&timeout=3s")
	if status != http.StatusOK {
		t.Fatalf("first wait status=%d body=%v", status, body)
	}
	start := time.Now()
	status, _, body = getJSON(t, control+"/sessions/idle/wait?quiet=500ms&timeout=3s")
	if status != http.StatusOK {
		t.Fatalf("second wait status=%d body=%v", status, body)
	}
	if elapsed := time.Since(start); elapsed >= 50*time.Millisecond {
		t.Fatalf("second wait took %s, want < 50ms", elapsed)
	}
}

func TestP6StrictQueryParams(t *testing.T) {
	_, control, _ := startControl(t)
	cases := []struct {
		path  string
		param string
	}{
		{"/sessions/x/wait?timeout_ms=1000", "timeout_ms"},
		{"/sessions/x/wait?quiet_for=0.2s", "quiet_for"},
		{"/sessions/x/wait?timeout=abc", "timeout"},
	}
	for _, tc := range cases {
		status, _, body := getJSON(t, control+tc.path)
		if status != http.StatusBadRequest {
			t.Fatalf("%s status=%d body=%v", tc.path, status, body)
		}
		errObj, _ := body["error"].(map[string]any)
		if fmtString(errObj["type"]) != "invalid_parameter" || fmtString(errObj["param"]) != tc.param {
			t.Fatalf("%s error=%v", tc.path, body["error"])
		}
	}
	for _, raw := range []string{"90", "90s"} {
		status, _, body := getJSON(t, control+"/sessions/equiv/wait?timeout="+raw+"&quiet=20ms")
		if status == http.StatusBadRequest {
			t.Fatalf("timeout=%s rejected: %v", raw, body)
		}
		if status != http.StatusGatewayTimeout {
			t.Fatalf("timeout=%s status=%d body=%v", raw, status, body)
		}
	}
}

func TestP7ControlErrorsAreJSON(t *testing.T) {
	proxyURL, control, _ := startControl(t)
	tokenCfg := proxy.Config{ListenAddr: ":0", ControlAddr: "127.0.0.1:0", ControlToken: "secret"}
	if err := tokenCfg.AddUpstream("default=http://example.test"); err != nil {
		t.Fatal(err)
	}
	tokServer, err := proxy.NewServer(tokenCfg, proxy.NewMemoryStore(), proxy.TolerantParser{})
	if err != nil {
		t.Fatal(err)
	}
	tokControl := httptest.NewServer(tokServer.ControlHandler())
	t.Cleanup(tokControl.Close)

	cases := []struct {
		name   string
		status int
		typ    string
		do     func() *http.Response
	}{
		{"400", 400, "invalid_parameter", func() *http.Response {
			res, err := http.Get(control + "/sessions/x/wait?timeout=abc")
			if err != nil {
				t.Fatal(err)
			}
			return res
		}},
		{"401", 401, "unauthorized", func() *http.Response {
			res, err := http.Get(tokControl.URL + "/sessions")
			if err != nil {
				t.Fatal(err)
			}
			return res
		}},
		{"404", 404, "session_not_found", func() *http.Response {
			res, err := http.Get(control + "/sessions/nope")
			if err != nil {
				t.Fatal(err)
			}
			return res
		}},
		{"405", 405, "method_not_allowed", func() *http.Response {
			req, err := http.NewRequest(http.MethodPost, control+"/sessions", nil)
			if err != nil {
				t.Fatal(err)
			}
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			return res
		}},
		{"504", 504, "wait_timeout", func() *http.Response {
			res, err := http.Get(control + "/sessions/ghost/wait?quiet=20ms&timeout=40ms")
			if err != nil {
				t.Fatal(err)
			}
			return res
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := tc.do()
			raw, err := io.ReadAll(res.Body)
			_ = res.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			if res.StatusCode != tc.status {
				t.Fatalf("status=%d want %d body=%s", res.StatusCode, tc.status, raw)
			}
			if !strings.Contains(res.Header.Get("Content-Type"), "application/json") {
				t.Fatalf("content-type=%q", res.Header.Get("Content-Type"))
			}
			var body map[string]any
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Fatal(err)
			}
			errObj, _ := body["error"].(map[string]any)
			if fmtString(errObj["type"]) != tc.typ {
				t.Fatalf("error.type=%v want %s", errObj["type"], tc.typ)
			}
		})
	}
	_ = proxyURL
}

func TestP8VersionFlagAndConfig(t *testing.T) {
	_, control, _ := startControl(t)
	status, _, body := getJSON(t, control+"/config")
	if status != http.StatusOK {
		t.Fatalf("config status=%d", status)
	}
	if fmtString(body["version"]) == "" {
		t.Fatalf("config missing version: %v", body)
	}
	status, _, ver := getJSON(t, control+"/version")
	if status != http.StatusOK || fmtString(ver["version"]) == "" {
		t.Fatalf("GET /version=%d %v", status, ver)
	}
	for _, flag := range []string{"--version", "-version"} {
		cmd := exec.Command(proxyBinary(t), flag)
		cmd.Env = append(os.Environ(), "LLMENTARY_SPEC_LOG_MAX=-1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", flag, err, out)
		}
		if cmd.ProcessState.ExitCode() != 0 {
			t.Fatalf("%s exit=%d", flag, cmd.ProcessState.ExitCode())
		}
		if !strings.HasPrefix(string(out), "llmentary ") {
			t.Fatalf("%s output=%q", flag, out)
		}
	}
}

func TestControlSessionIDEscapedOnce(t *testing.T) {
	proxyURL, control, _ := startControl(t)
	id := "a%b"
	postCall(t, proxyURL, id)
	escaped := url.PathEscape(id)
	status, _, body := getJSON(t, control+"/sessions/"+escaped)
	if status != http.StatusOK {
		t.Fatalf("GET escaped session status=%d body=%v", status, body)
	}
	if fmtString(body["id"]) != id {
		t.Fatalf("session id=%v want %q", body["id"], id)
	}
	status, _, _ = getJSON(t, control+"/sessions/a/b")
	if status != http.StatusNotFound {
		t.Fatalf("literal slash path should 404, got %d", status)
	}
}

func TestP9OpenAPIRouteSync(t *testing.T) {
	if len(proxy.ControlRoutes) == 0 {
		t.Fatal("ControlRoutes empty")
	}
}

func fmtString(v any) string {
	if v == nil {
		return ""
	}
	s, ok := v.(string)
	if ok {
		return s
	}
	b, _ := json.Marshal(v)
	return strings.Trim(string(b), `"`)
}

func intFrom(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	default:
		return -1
	}
}
