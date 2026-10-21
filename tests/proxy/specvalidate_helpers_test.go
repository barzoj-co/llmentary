// SPDX-License-Identifier: AGPL-3.0-only

package proxy_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/barzoj-co/llmentary/internal/proxy"
)

type fakeUpstream struct {
	mu       sync.Mutex
	count    int
	bodies   [][]byte
	lastResp []byte
	handler  http.HandlerFunc
}

type capturingWriter struct {
	http.ResponseWriter
	buf bytes.Buffer
}

func (c *capturingWriter) Write(p []byte) (int, error) {
	_, _ = c.buf.Write(p)
	return c.ResponseWriter.Write(p)
}

func (f *fakeUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	cw := &capturingWriter{ResponseWriter: w}
	f.mu.Lock()
	f.count++
	f.bodies = append(f.bodies, append([]byte(nil), body...))
	h := f.handler
	f.mu.Unlock()
	if h != nil {
		h(cw, r)
	} else {
		cw.Header().Set("Content-Type", "application/json")
		cw.WriteHeader(http.StatusOK)
		_, _ = cw.Write([]byte(`{"id":"up","model":"gpt-fixture","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}
	f.mu.Lock()
	f.lastResp = append([]byte(nil), cw.buf.Bytes()...)
	f.mu.Unlock()
}

func (f *fakeUpstream) Count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.count
}

func (f *fakeUpstream) LastBody() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.bodies) == 0 {
		return nil
	}
	return f.bodies[len(f.bodies)-1]
}

func (f *fakeUpstream) LastResponse() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]byte(nil), f.lastResp...)
}

type proxyProc struct {
	proxyURL   string
	controlURL string
	upstream   *fakeUpstream
}

func startSpecProxy(t *testing.T, mode, profile string, upstream http.HandlerFunc, extra ...func(*proxy.Config)) proxyProc {
	t.Helper()
	return startSpecProxyStore(t, proxy.NewMemoryStore(), mode, profile, upstream, extra...)
}

func startSpecProxyStore(t *testing.T, store *proxy.MemoryStore, mode, profile string, upstream http.HandlerFunc, extra ...func(*proxy.Config)) proxyProc {
	t.Helper()
	fake := &fakeUpstream{handler: upstream}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	cfg := proxy.Config{
		ListenAddr:     ":0",
		ControlAddr:    "127.0.0.1:0",
		SpecValidation: mode,
		SpecProfile:    profile,
	}
	if err := cfg.AddUpstream("default=" + srv.URL); err != nil {
		t.Fatal(err)
	}
	for _, fn := range extra {
		fn(&cfg)
	}
	if store == nil {
		store = proxy.NewMemoryStore()
	}
	server, err := proxy.NewServer(cfg, store, proxy.TolerantParser{})
	if err != nil {
		t.Fatal(err)
	}
	proxySrv := httptest.NewServer(server.ProxyHandler())
	t.Cleanup(proxySrv.Close)
	control := httptest.NewServer(server.ControlHandler())
	t.Cleanup(control.Close)
	return proxyProc{proxyURL: proxySrv.URL, controlURL: control.URL, upstream: fake}
}

func postRawErr(p proxyProc, path, session string, headers map[string]string, body []byte) (status int, respHeaders http.Header, respBody []byte, trace map[string]any, err error) {
	req, err := http.NewRequest(http.MethodPost, p.proxyURL+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(proxy.SessionHeaderName, session)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client := &http.Client{Transport: &http.Transport{DisableCompression: true}}
	res, err := client.Do(req)
	if err != nil {
		return 0, nil, nil, nil, err
	}
	raw, err := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if err != nil {
		return 0, nil, nil, nil, err
	}
	wait, err := http.NewRequestWithContext(context.Background(), http.MethodGet, p.controlURL+"/sessions/"+session+"/wait?quiet=20ms&timeout=3s", nil)
	if err != nil {
		return 0, nil, nil, nil, err
	}
	wres, err := http.DefaultClient.Do(wait)
	if err != nil {
		return 0, nil, nil, nil, err
	}
	wraw, err := io.ReadAll(wres.Body)
	_ = wres.Body.Close()
	if err != nil {
		return 0, nil, nil, nil, err
	}
	var sessionJSON map[string]any
	if err := json.Unmarshal(wraw, &sessionJSON); err != nil {
		return 0, nil, nil, nil, fmt.Errorf("session json: %v body=%s", err, wraw)
	}
	return res.StatusCode, res.Header.Clone(), raw, sessionJSON, nil
}

func postRaw(t *testing.T, p proxyProc, path, session string, headers map[string]string, body []byte) (status int, respHeaders http.Header, respBody []byte, trace map[string]any) {
	t.Helper()
	status, respHeaders, respBody, trace, err := postRawErr(p, path, session, headers, body)
	if err != nil {
		t.Fatal(err)
	}
	return status, respHeaders, respBody, trace
}

func lastCallErr(session map[string]any) (map[string]any, error) {
	calls, _ := session["calls"].([]any)
	if len(calls) == 0 {
		return nil, fmt.Errorf("no calls in session")
	}
	call, _ := calls[len(calls)-1].(map[string]any)
	if call == nil {
		return nil, fmt.Errorf("last call is not an object")
	}
	return call, nil
}

func lastCall(t *testing.T, session map[string]any) map[string]any {
	t.Helper()
	call, err := lastCallErr(session)
	if err != nil {
		t.Fatal(err)
	}
	return call
}

func normalized(call map[string]any) map[string]any {
	n, _ := call["normalized"].(map[string]any)
	return n
}

func rawJSONHasKey(raw []byte, key string) bool {
	return bytes.Contains(raw, []byte(`"`+key+`"`))
}

func sessionRaw(t *testing.T, controlURL, session string) []byte {
	t.Helper()
	res, err := http.Get(controlURL + "/sessions/" + session)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func getConfig(t *testing.T, controlURL string) map[string]any {
	t.Helper()
	res, err := http.Get(controlURL + "/config")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func chatOK() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"c","object":"chat.completion","created":1,"model":"gpt-fixture","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop","logprobs":null}]}`))
	}
}

func responsesOK() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"r","model":"gpt-fixture","status":"completed","output":[{"id":"m","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok","annotations":[],"logprobs":[]}]}],"tools":[{"type":"function","name":"x","strict":false,"parameters":{}}]}`))
	}
}

var (
	binOnce sync.Once
	binPath string
	binErr  error
)

func proxyBinary(t *testing.T) string {
	t.Helper()
	binOnce.Do(func() {
		dir, err := os.MkdirTemp("", "llmentary-bin-")
		if err != nil {
			binErr = err
			return
		}
		binPath = filepath.Join(dir, "llmentary")
		cmd := exec.Command("go", "build", "-o", binPath, "./cmd/llmentary")
		cmd.Dir = filepath.Join("..", "..")
		out, err := cmd.CombinedOutput()
		if err != nil {
			binErr = fmt.Errorf("build proxy: %w\n%s", err, out)
		}
	})
	if binErr != nil {
		t.Fatal(binErr)
	}
	return binPath
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port
}

func startBinary(t *testing.T, args []string, env []string) (proxyURL, controlURL string, cmd *exec.Cmd) {
	t.Helper()
	pPort, cPort := freePort(t), freePort(t)
	all := append([]string{
		"--listen", fmt.Sprintf("127.0.0.1:%d", pPort),
		"--control", fmt.Sprintf("127.0.0.1:%d", cPort),
	}, args...)
	cmd = exec.Command(proxyBinary(t), all...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	proxyURL = fmt.Sprintf("http://127.0.0.1:%d", pPort)
	controlURL = fmt.Sprintf("http://127.0.0.1:%d", cPort)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		res, err := http.Get(controlURL + "/healthz")
		if err == nil {
			_, _ = io.Copy(io.Discard, res.Body)
			_ = res.Body.Close()
			if res.StatusCode == 200 {
				return proxyURL, controlURL, cmd
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("proxy binary did not become healthy")
	return "", "", cmd
}

var sessionSeq atomic.Int64

func newSession() string {
	return fmt.Sprintf("s%d", sessionSeq.Add(1))
}

func trailingJSONOffset(body []byte) int {
	dec := json.NewDecoder(bytes.NewReader(body))
	var v json.RawMessage
	if err := dec.Decode(&v); err != nil {
		return int(dec.InputOffset())
	}
	off := int(dec.InputOffset())
	if off < 0 {
		off = 0
	}
	if off > len(body) {
		off = len(body)
	}
	for i, c := range body[off:] {
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			return off + i
		}
	}
	return -1
}

func assertIgnoredExact(t *testing.T, n map[string]any, want map[string]any) {
	t.Helper()
	var found map[string]any
	for _, v := range asAnySlice(n["ignored_fields"]) {
		m, _ := v.(map[string]any)
		if m["key"] != want["key"] {
			continue
		}
		if p, ok := want["pointer"]; ok && p != "" && m["pointer"] != p {
			continue
		}
		found = m
		break
	}
	if found == nil {
		t.Fatalf("missing ignored_fields %v in %v", want, n["ignored_fields"])
	}
	for k, exp := range want {
		if fmt.Sprint(found[k]) != fmt.Sprint(exp) {
			t.Fatalf("ignored_fields.%s=%v want %v (entry=%v)", k, found[k], exp, found)
		}
	}
}
