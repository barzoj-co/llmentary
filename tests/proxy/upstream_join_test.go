// SPDX-License-Identifier: AGPL-3.0-only

package proxy_test

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/barzoj-co/llmentary/internal/proxy"
)

func startAcceptanceSpec(t *testing.T, spec string, upstream http.HandlerFunc) (string, string, *capturedUpstream) {
	t.Helper()
	got := &capturedUpstream{}
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.method = r.Method
		got.path = r.URL.Path
		got.query = r.URL.RawQuery
		got.headers = r.Header.Clone()
		body, _ := io.ReadAll(r.Body)
		got.body = body
		upstream(w, r)
	}))
	t.Cleanup(fake.Close)
	cfg := proxy.Config{ListenAddr: ":0", ControlAddr: "127.0.0.1:0"}
	resolved := strings.ReplaceAll(spec, "{upstream}", fake.URL)
	if err := cfg.AddUpstream(resolved); err != nil {
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
	return proxySrv.URL, control.URL, got
}

func TestU3EchoJoinStripsDuplicateV1(t *testing.T) {
	req := []byte(`{"model":"gpt-fixture","messages":[{"role":"user","content":"hi"}]}`)
	proxyURL, control, got := startAcceptanceSpec(t, "openrouter={upstream}/api/v1", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, `{"id":"c","model":"gpt-fixture","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`, nil)
	})
	rt := doProxy(t, http.MethodPost, proxyURL, control, "/v1/chat/completions", nil, req)
	if rt.status != http.StatusOK {
		t.Fatalf("status=%d body=%s", rt.status, rt.body)
	}
	if got.path != "/api/v1/chat/completions" {
		t.Fatalf("upstream path=%q want /api/v1/chat/completions", got.path)
	}
}

func TestU5OpenRouterFixture(t *testing.T) {
	stream, err := testdataRead(filepath.Join("openrouter", "chat_stream_tool.sse"))
	if err != nil {
		t.Fatal(err)
	}
	req := []byte(`{"model":"openai/gpt-4.1-mini","stream":true,"messages":[{"role":"user","content":"weather in Paris?"}],"tools":[{"type":"function","function":{"name":"get_weather","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}}]}`)
	proxyURL, control, got := startAcceptanceSpec(t, "openrouter={upstream}/api/v1", func(w http.ResponseWriter, _ *http.Request) {
		writeSSE(w, string(stream))
	})
	rt := doProxy(t, http.MethodPost, proxyURL, control, "/u/openrouter/chat/completions", nil, req)
	if rt.status != http.StatusOK {
		t.Fatalf("status=%d body=%s", rt.status, rt.body)
	}
	if got.path != "/api/v1/chat/completions" {
		t.Fatalf("upstream path=%q want /api/v1/chat/completions", got.path)
	}
	n := rt.call.Normalized
	if n.ServedBy != "OpenAI" {
		t.Fatalf("served_by=%q want OpenAI", n.ServedBy)
	}
	if n.OutputText != "I'll check." {
		t.Fatalf("output_text=%q", n.OutputText)
	}
	if n.FinishReason != "tool_calls" {
		t.Fatalf("finish_reason=%q", n.FinishReason)
	}
	if len(n.ToolCalls) != 1 || n.ToolCalls[0].Name != "get_weather" || string(n.ToolCalls[0].Arguments) != `{"city":"Paris"}` {
		t.Fatalf("tool_calls=%#v", n.ToolCalls)
	}
	if n.Usage == nil || n.Usage.TotalTokens == nil || *n.Usage.TotalTokens != 20 {
		t.Fatalf("usage=%+v", n.Usage)
	}
	if n.Usage.CostUSD == nil || *n.Usage.CostUSD != 0.00042 {
		t.Fatalf("usage.cost=%+v", n.Usage)
	}
}

func TestU1DefaultUpstreamBinaryAndConfig(t *testing.T) {
	logBuf := &syncBuffer{}
	pPort, cPort := freePort(t), freePort(t)
	cmd := exec.Command(proxyBinary(t),
		"--listen", "127.0.0.1:"+strconv.Itoa(pPort),
		"--control", "127.0.0.1:"+strconv.Itoa(cPort),
	)
	cmd.Env = envWithout("LLMENTARY_UPSTREAMS", "LLMENTARY_UPSTREAM")
	cmd.Stderr = logBuf
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	controlURL := "http://127.0.0.1:" + strconv.Itoa(cPort)
	waitHealthy(t, controlURL)
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(logBuf.String(), proxy.DefaultUpstreamLog) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(logBuf.String(), proxy.DefaultUpstreamLog) {
		t.Fatalf("startup log %q does not contain %q", logBuf.String(), proxy.DefaultUpstreamLog)
	}
	cfg := getConfig(t, controlURL)
	upstreams, _ := cfg["upstreams"].(map[string]any)
	if stringVal(upstreams[proxy.DefaultUpstreamName]) != proxy.DefaultUpstreamURL {
		t.Fatalf("config upstreams=%v", cfg["upstreams"])
	}
}

func TestU1DefaultUpstreamInProcess(t *testing.T) {
	var logBuf bytes.Buffer
	cfg := proxy.Config{
		ListenAddr:  ":0",
		ControlAddr: "127.0.0.1:0",
		Logger:      slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelInfo})),
	}
	server, err := proxy.NewServer(cfg, proxy.NewMemoryStore(), proxy.TolerantParser{})
	if err != nil {
		t.Fatal(err)
	}
	control := httptest.NewServer(server.ControlHandler())
	t.Cleanup(control.Close)
	got := getConfig(t, control.URL)
	upstreams, _ := got["upstreams"].(map[string]any)
	if stringVal(upstreams[proxy.DefaultUpstreamName]) != proxy.DefaultUpstreamURL {
		t.Fatalf("config=%v", got)
	}
	if !strings.Contains(logBuf.String(), proxy.DefaultUpstreamLog) {
		t.Fatalf("log=%q", logBuf.String())
	}
}

func TestU4LoopGuardCLI(t *testing.T) {
	port := freePort(t)
	cmd := exec.Command(proxyBinary(t),
		"--listen", "127.0.0.1:"+strconv.Itoa(port),
		"--upstream", "openai=http://127.0.0.1:"+strconv.Itoa(port)+"/v1",
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected non-zero exit for looped upstream")
	}
	if !bytes.Contains(out, []byte("proxy loop")) {
		t.Fatalf("message=%s", out)
	}
}

func TestDocsUpstreamJoinNoDoubleV1(t *testing.T) {
	files := []string{
		filepath.Join("..", "..", "docs", "proxy.md"),
		filepath.Join("..", "..", "docs", "providers.md"),
	}
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		if strings.Contains(text, "/v1/v1") {
			t.Errorf("%s contains /v1/v1", path)
		}
		for _, pair := range extractDocJoinPairs(text) {
			base, err := url.Parse(pair.upstream)
			if err != nil {
				t.Fatalf("%s: upstream %q: %v", path, pair.upstream, err)
			}
			got := proxy.JoinUpstreamURL(base, pair.requestPath)
			if strings.Contains(got.Path, "/v1/v1") {
				t.Errorf("%s: %s + %s joined to %s", path, pair.upstream, pair.requestPath, got.Path)
			}
			if pair.wantPath != "" && got.Path != pair.wantPath {
				t.Errorf("%s: %s + %s => %s want %s", path, pair.upstream, pair.requestPath, got.Path, pair.wantPath)
			}
		}
	}
}

type docJoinPair struct {
	upstream    string
	requestPath string
	wantPath    string
}

func extractDocJoinPairs(text string) []docJoinPair {
	var out []docJoinPair
	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines); i++ {
		if !strings.Contains(lines[i], "Upstream form") || !strings.Contains(lines[i], "Client base") {
			continue
		}
		i++
		if i < len(lines) && strings.Contains(lines[i], "---") {
			i++
		}
		for i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), "|") {
			cells := splitMarkdownRow(lines[i])
			i++
			if len(cells) < 3 {
				continue
			}
			upstream := strings.Trim(cells[0], " `")
			client := strings.Trim(cells[1], " `")
			final := strings.Trim(cells[len(cells)-1], " `")
			reqPath := clientBaseToRequestPath(client)
			wantPath := ""
			if u, err := url.Parse(final); err == nil && u.Host != "" {
				wantPath = u.Path
			}
			if strings.HasPrefix(upstream, "http") && reqPath != "" {
				out = append(out, docJoinPair{upstream: upstream, requestPath: reqPath, wantPath: wantPath})
			}
		}
	}
	return out
}

func splitMarkdownRow(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")
	parts := strings.Split(line, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func clientBaseToRequestPath(client string) string {
	client = strings.TrimSpace(client)
	client = strings.ReplaceAll(client, "{proxy}", "http://127.0.0.1:7777")
	client = strings.ReplaceAll(client, "{id}", "t1")
	if strings.HasPrefix(client, "http") {
		if u, err := url.Parse(client); err == nil {
			client = u.Path
		}
	}
	if client == "" {
		return ""
	}
	if !strings.HasPrefix(client, "/") {
		client = "/" + client
	}
	switch {
	case strings.HasSuffix(client, "/v1"),
		strings.HasSuffix(client, "/u/openai"),
		strings.HasSuffix(client, "/u/openrouter"),
		strings.HasSuffix(client, "/u/azure"):
		client = strings.TrimRight(client, "/") + "/chat/completions"
	}
	return stripDocRoutingPrefixes(client)
}

func stripDocRoutingPrefixes(p string) string {
	clean := "/" + strings.TrimLeft(p, "/")
	for {
		changed := false
		for _, prefix := range []string{"/u/", "/s/"} {
			if !strings.HasPrefix(clean, prefix) {
				continue
			}
			rest := strings.TrimLeft(clean[len(prefix):], "/")
			_, after, ok := strings.Cut(rest, "/")
			if !ok {
				clean = "/"
			} else {
				clean = "/" + after
			}
			changed = true
			break
		}
		if !changed {
			return clean
		}
	}
}

func envWithout(keys ...string) []string {
	drop := make(map[string]bool, len(keys))
	for _, k := range keys {
		drop[k] = true
	}
	out := make([]string, 0, len(os.Environ()))
	for _, e := range os.Environ() {
		k, _, _ := strings.Cut(e, "=")
		if drop[k] {
			continue
		}
		out = append(out, e)
	}
	return out
}

func waitHealthy(t *testing.T, controlURL string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		res, err := http.Get(controlURL + "/healthz")
		if err == nil {
			_, _ = io.Copy(io.Discard, res.Body)
			_ = res.Body.Close()
			if res.StatusCode == 200 {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("proxy binary did not become healthy")
}

func stringVal(v any) string {
	s, _ := v.(string)
	return s
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestConfigJSONRedactsUpstreamUserinfo(t *testing.T) {
	cfg := proxy.Config{ListenAddr: ":0", ControlAddr: "127.0.0.1:0"}
	if err := cfg.AddUpstream("litellm=http://user:secret@127.0.0.1:9/v1"); err != nil {
		t.Fatal(err)
	}
	server, err := proxy.NewServer(cfg, proxy.NewMemoryStore(), proxy.TolerantParser{})
	if err != nil {
		t.Fatal(err)
	}
	control := httptest.NewServer(server.ControlHandler())
	t.Cleanup(control.Close)
	got := getConfig(t, control.URL)
	upstreams, _ := got["upstreams"].(map[string]any)
	raw := stringVal(upstreams["litellm"])
	if strings.Contains(raw, "secret") {
		t.Fatalf("userinfo leaked in /config: %q", raw)
	}
	if !strings.Contains(raw, "xxxxx") {
		t.Fatalf("expected redacted userinfo in /config, got %q", raw)
	}
}
