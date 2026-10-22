// SPDX-License-Identifier: AGPL-3.0-only

package compat_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/barzoj-co/llmentary/internal/proxy"
)

func TestCompatibleNoisyFixturesAreSpecClean(t *testing.T) {
	compat := startCompatProxy(t, "warn", "relaxed", proxy.SpecTargetCompatible)
	openai := startCompatProxy(t, "warn", "relaxed", proxy.SpecTargetOpenAI)
	cases := []struct {
		provider, surface string
		compatShape       bool
		vendorNeedle      string
	}{
		{"together", "chat.json", true, ""},
		{"openrouter", "chat.json", true, "provider"},
		{"openrouter", "chat.sse", true, "provider"},
		{"groq", "chat.json", false, "x_groq"},
		{"llamacpp", "chat.json", false, "timings"},
		{"deepseek", "chat.json", true, ""},
		{"together", "responses.json", true, ""},
		{"fixture", "responses.sse", true, ""},
		{"ollama", "chat.json", false, ""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.provider+"/"+tc.surface, func(t *testing.T) {
			path, req := requestFor(tc.provider, tc.surface)
			status, call := postCase(t, compat, path, newSession(), req)
			if status != http.StatusOK {
				t.Fatalf("status=%d", status)
			}
			if specClass(call) != "clean" {
				t.Fatalf("compat spec=%s vs=%v", specClass(call), specViolations(call))
			}
			if tc.compatShape && countKeyword(specViolations(call), "response_shape") == 0 {
				t.Fatalf("expected stored response_shape: %v", specViolations(call))
			}
			if tc.vendorNeedle != "" {
				for _, v := range specViolations(call) {
					blob := fmt.Sprint(v["message"], v["pointer"], v["path"])
					if strings.Contains(blob, tc.vendorNeedle) {
						t.Fatalf("vendor extra %q produced a finding: %v", tc.vendorNeedle, v)
					}
				}
			}

			ostatus, ocall := postCase(t, openai, path, newSession(), req)
			if ostatus != http.StatusOK {
				t.Fatalf("openai status=%d", ostatus)
			}
			if tc.provider == "together" && tc.surface == "chat.json" && specClass(ocall) != "findings" {
				t.Fatalf("openai target dropped finish_reason findings: %v", specViolations(ocall))
			}
			if tc.surface == "responses.json" && specClass(ocall) != "findings" {
				t.Fatalf("openai responses stayed clean: %v", specViolations(ocall))
			}
		})
	}
}

func TestCompatibleVendorRequestIsCleanAndForwards(t *testing.T) {
	compat := startCompatProxy(t, "enforce", "relaxed", proxy.SpecTargetCompatible)
	openai := startCompatProxy(t, "enforce", "relaxed", proxy.SpecTargetOpenAI)
	req := chatRequest("ollama", `"top_k":20`)

	status, call := postCase(t, compat, chatJSONPath, newSession(), req)
	if status != http.StatusOK || !asBool(call["upstream_called"]) {
		t.Fatalf("compat top_k status=%d called=%v", status, call["upstream_called"])
	}
	if specClass(call) != "clean" {
		t.Fatalf("compat top_k spec=%s vs=%v", specClass(call), specViolations(call))
	}

	ostatus, ocall := postCase(t, openai, chatJSONPath, newSession(), req)
	if ostatus != http.StatusBadRequest || asBool(ocall["upstream_called"]) {
		t.Fatalf("openai top_k status=%d called=%v", ostatus, ocall["upstream_called"])
	}
	if specClass(ocall) != "findings" {
		t.Fatalf("openai top_k spec=%s vs=%v", specClass(ocall), specViolations(ocall))
	}
}

func specViolations(call map[string]any) []map[string]any {
	n, _ := call["normalized"].(map[string]any)
	if n == nil {
		return nil
	}
	raw, _ := n["spec_violations"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		m, _ := item.(map[string]any)
		if m != nil {
			out = append(out, m)
		}
	}
	return out
}

func countKeyword(vs []map[string]any, keyword string) int {
	n := 0
	for _, v := range vs {
		if fmt.Sprint(v["keyword"]) == keyword {
			n++
		}
	}
	return n
}
