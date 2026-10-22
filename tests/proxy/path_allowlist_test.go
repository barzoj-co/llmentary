// SPDX-License-Identifier: AGPL-3.0-only

package proxy_test

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/barzoj-co/llmentary/internal/proxy"
)

func TestNamedUpstreamSkipsPathAllowList(t *testing.T) {
	paths := []string{
		"/completion", "/tokenize", "/infill", "/detokenize", "/apply-template",
		"/health", "/props", "/slots", "/rerank", "/pooling", "/score",
		"/metrics", "/version",
		"/beta/chat/completions", "/beta/completions",
		"/v1beta/openai/chat/completions",
		"/inference/v1/chat/completions",
		"/client/v4/accounts/abc/ai/v1/chat/completions",
		"/COMPLETION", "/tokenize/",
	}
	echo := &echoUpstream{}
	proxyURL, control := startPathProxy(t, echo, func(cfg *proxy.Config) {
		if err := cfg.AddUpstream("llama=" + cfg.Upstreams["openai"].String()); err != nil {
			t.Fatal(err)
		}
	})
	body := []byte(`{"model":"m","prompt":"hi"}`)
	for _, path := range paths {
		for _, method := range []string{http.MethodPost, http.MethodGet} {
			t.Run(method+path, func(t *testing.T) {
				echo.mu.Lock()
				echo.hits = 0
				echo.mu.Unlock()
				named := "/u/llama" + strings.ToLower(path)
				if strings.HasPrefix(path, "/COMPLETION") {
					named = "/u/llama/COMPLETION"
				}
				status, _, raw := doMethod(t, method, proxyURL+named, body)
				if status != http.StatusOK {
					t.Fatalf("named %s: %d %s", named, status, raw)
				}
				sid := "p1"
				sessPath := "/s/" + sid + named
				status, _, raw = doMethod(t, method, proxyURL+sessPath, body)
				if status != http.StatusOK {
					t.Fatalf("session+named %s: %d %s", sessPath, status, raw)
				}
			})
		}
	}

	status, _, raw := doMethod(t, http.MethodPost, proxyURL+"/u/llama/beta/chat/completions", []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`))
	if status != http.StatusOK {
		t.Fatalf("beta chat: %d %s", status, raw)
	}
	calls := waitCalls(t, control, "default")
	if len(calls) == 0 {
		t.Fatal("no calls")
	}
	// Last recorded call on default session should be the beta chat POST.
	last := calls[len(calls)-1]
	if last.APIFamily != "openai.chat" {
		t.Fatalf("api_family=%s want openai.chat", last.APIFamily)
	}
}

func TestBareProviderPathsNeedAllowPrefix(t *testing.T) {
	echo := &echoUpstream{}
	proxyURL, _ := startPathProxy(t, echo)
	status, _, raw := doMethod(t, http.MethodPost, proxyURL+"/tokenize", []byte(`{}`))
	if status != http.StatusNotFound {
		t.Fatalf("bare tokenize should 404: %d %s", status, raw)
	}
	if !bytes.Contains(raw, []byte("llmentary_unknown_path")) || !bytes.Contains(raw, []byte("--allow-path-prefix tokenize")) {
		t.Fatalf("hint missing: %s", raw)
	}
	if !bytes.Contains(raw, []byte("LLMENTARY_ALLOW_PATH_PREFIXES")) {
		t.Fatalf("env missing: %s", raw)
	}
	if hits, _, _, _, _ := echo.snapshot(); hits != 0 {
		t.Fatalf("forwarded=%d", hits)
	}

	status, _, raw = doMethod(t, http.MethodPost, proxyURL+"/beta/chat/completions", []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`))
	if status != http.StatusOK {
		t.Fatalf("bare beta should be allowed: %d %s", status, raw)
	}
	status, _, raw = doMethod(t, http.MethodPost, proxyURL+"/v1beta/openai/chat/completions", []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`))
	if status != http.StatusOK {
		t.Fatalf("bare v1beta should be allowed: %d %s", status, raw)
	}
	status, _, raw = doMethod(t, http.MethodPost, proxyURL+"/completion", []byte(`{}`))
	if status != http.StatusNotFound || !bytes.Contains(raw, []byte("--allow-path-prefix completion")) {
		t.Fatalf("bare completion: %d %s", status, raw)
	}

	status, _, raw = doMethod(t, http.MethodPost, proxyURL+"/foo%2fbar/x", []byte(`{}`))
	if status != http.StatusNotFound {
		t.Fatalf("encoded slash should 404: %d %s", status, raw)
	}
}

func TestBarePathAllowPrefixOverride(t *testing.T) {
	echo := &echoUpstream{}
	proxyURL, _ := startPathProxy(t, echo, func(cfg *proxy.Config) {
		cfg.AllowPathPrefixes = []string{"tokenize"}
	})
	status, _, raw := doMethod(t, http.MethodPost, proxyURL+"/tokenize", []byte(`{"prompt":"hi"}`))
	if status != http.StatusOK {
		t.Fatalf("allow-path-prefix tokenize: %d %s", status, raw)
	}
}

func doMethod(t *testing.T, method, url string, body []byte) (int, http.Header, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
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
