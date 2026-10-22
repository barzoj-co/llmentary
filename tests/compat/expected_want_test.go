// SPDX-License-Identifier: AGPL-3.0-only

package compat_test

import (
	"strings"
	"testing"

	"github.com/barzoj-co/llmentary/internal/proxy"
)

func TestExpectedWantCompatibleStaysClean(t *testing.T) {
	t.Parallel()
	req := chatRequest("together", `"top_k":20`)
	for _, provider := range providers {
		for _, surface := range append(append([]string{}, surfaces...), "chat.json.top_k") {
			surface := surface
			provider := provider
			got := expectedWant(proxy.SpecTargetCompatible, "enforce", surface, provider, req)
			if got.Status != 200 || !got.UpstreamCalled || got.Spec != "clean" || got.Reject {
				t.Fatalf("%s %s: %#v (compat target must not count response_shape as findings)", provider, surface, got)
			}
			warn := expectedWant(proxy.SpecTargetCompatible, "warn", surface, provider, chatRequest(provider, ""))
			if warn.Spec != "clean" || warn.Status != 200 || !warn.UpstreamCalled {
				t.Fatalf("%s %s warn: %#v", provider, surface, warn)
			}
		}
	}
}

func TestExpectedWantOpenAIKeepsResponseFindings(t *testing.T) {
	t.Parallel()
	cases := []struct {
		provider, surface, spec string
	}{
		{"fixture", "chat.json", "clean"},
		{"fixture", "chat.sse", "clean"},
		{"fixture", "chat.tools", "clean"},
		{"fixture", "responses.json", "findings"},
		{"fixture", "responses.sse", "findings"},
		{"together", "chat.json", "findings"},
		{"openrouter", "chat.json", "findings"},
		{"groq", "chat.json", "findings"},
		{"deepseek", "chat.json", "findings"},
		{"ollama", "chat.json", "findings"},
		{"llamacpp", "chat.json", "findings"},
		{"mistral", "chat.json", "findings"},
		{"gemini", "chat.json", "findings"},
		{"together", "chat.sse", "findings"},
		{"openrouter", "chat.sse", "findings"},
		{"groq", "chat.sse", "clean"},
		{"ollama", "chat.sse", "clean"},
		{"together", "responses.json", "findings"},
	}
	for _, tc := range cases {
		got := expectedWant(proxy.SpecTargetOpenAI, "warn", tc.surface, tc.provider, chatRequest(tc.provider, ""))
		if got.Spec != tc.spec || got.Status != 200 || !got.UpstreamCalled || got.Reject {
			t.Fatalf("%s %s: %#v want spec=%s", tc.provider, tc.surface, got, tc.spec)
		}
	}
}

func TestExpectedWantOpenAIRejectsVendorRequest(t *testing.T) {
	t.Parallel()
	req := chatRequest("ollama", `"top_k":20`)
	got := expectedWant(proxy.SpecTargetOpenAI, "enforce", "chat.json", "ollama", req)
	if got.Status != 400 || got.UpstreamCalled || got.Spec != "findings" || !got.Reject {
		t.Fatalf("openai enforce top_k: %#v", got)
	}
	warn := expectedWant(proxy.SpecTargetOpenAI, "warn", "chat.json", "ollama", req)
	if warn.Status != 200 || !warn.UpstreamCalled || warn.Spec != "findings" || warn.Reject {
		t.Fatalf("openai warn top_k still forwards: %#v", warn)
	}
	compat := expectedWant(proxy.SpecTargetCompatible, "enforce", "chat.json", "ollama", req)
	if compat.Status != 200 || !compat.UpstreamCalled || compat.Spec != "clean" || compat.Reject {
		t.Fatalf("compat enforce top_k: %#v", compat)
	}
}

func TestBuildMatrixCompatibleNeverFindings(t *testing.T) {
	t.Parallel()
	cases := buildMatrix()
	if len(cases) < minMatrixCases {
		t.Fatalf("len=%d", len(cases))
	}
	var compat, openaiFindings int
	for _, c := range cases {
		if c.Target == proxy.SpecTargetCompatible {
			compat++
			if c.Want.Spec != "clean" || c.Want.Status != 200 || !c.Want.UpstreamCalled || c.Want.Reject {
				t.Fatalf("compat case %s: %#v", c.ID, c.Want)
			}
			continue
		}
		if c.Want.Spec == "findings" {
			openaiFindings++
		}
	}
	if compat == 0 || openaiFindings == 0 {
		t.Fatalf("compat=%d openaiFindings=%d", compat, openaiFindings)
	}
}

func TestRenderCompatMatrixMentionsResponseShape(t *testing.T) {
	t.Parallel()
	doc := renderCompatMatrix(buildMatrix())
	for _, needle := range []string{
		"response_shape",
		"openai-compatible",
		"vendor extras produce no finding",
		"| `together/chat.json/compat/warn/relaxed` |",
		"| together | chat.json | openai-compatible | warn | 200 | clean |",
		"| together | chat.json | openai | warn | 200 | findings |",
	} {
		if !strings.Contains(doc, needle) {
			t.Fatalf("missing %q in\n%s", needle, doc[:400])
		}
	}
}
