// SPDX-License-Identifier: AGPL-3.0-only

package compat_test

import (
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/barzoj-co/llmentary/internal/proxy"
)

// TestLocalEngineFixtures starts the in-process fixture server under the
// local-engine names from COMPAT-9 (llama.cpp, Ollama, deepseek-recipe) and
// drives JSON + SSE through the proxy. Real binaries are optional: set
// LLMENTARY_COMPAT_ENGINES=1 and put health URLs in
// LLMENTARY_COMPAT_ENGINE_URLS (comma-separated) to also probe those
// listeners. Model output content is never asserted.
func TestLocalEngineFixtures(t *testing.T) {
	p := startCompatProxy(t, "warn", "relaxed", proxy.SpecTargetCompatible)
	for _, provider := range []string{"llamacpp", "ollama", "deepseek"} {
		for _, surface := range []string{"chat.json", "chat.sse", "responses.json"} {
			path, req := requestFor(provider, surface)
			status, call := postCase(t, p, path, newSession(), req)
			if status != http.StatusOK {
				t.Fatalf("%s %s status=%d", provider, surface, status)
			}
			if !asBool(call["upstream_called"]) {
				t.Fatalf("%s %s upstream not called", provider, surface)
			}
			if n, _ := call["normalized"].(map[string]any); n != nil {
				if _, ok := n["output_text"].(string); ok {
					// Presence is fine; never assert the text.
				}
			}
		}
	}
}

func TestOptionalExternalEngines(t *testing.T) {
	if os.Getenv("LLMENTARY_COMPAT_ENGINES") != "1" {
		t.Skip("set LLMENTARY_COMPAT_ENGINES=1 to health-check real local engines")
	}
	raw := os.Getenv("LLMENTARY_COMPAT_ENGINE_URLS")
	if raw == "" {
		t.Skip("LLMENTARY_COMPAT_ENGINE_URLS is empty")
	}
	client := &http.Client{Timeout: 2 * time.Second}
	for _, url := range splitComma(raw) {
		var last error
		ok := false
		for i := 0; i < 8; i++ {
			res, err := client.Get(url)
			if err == nil {
				_ = res.Body.Close()
				if res.StatusCode < 500 {
					ok = true
					break
				}
				last = err
			} else {
				last = err
			}
			time.Sleep(200 * time.Millisecond)
		}
		if !ok {
			t.Fatalf("engine %s not healthy: %v", url, last)
		}
	}
}

func splitComma(raw string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(raw); i++ {
		if i == len(raw) || raw[i] == ',' {
			s := raw[start:i]
			for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
				s = s[1:]
			}
			for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
				s = s[:len(s)-1]
			}
			if s != "" {
				out = append(out, s)
			}
			start = i + 1
		}
	}
	return out
}
