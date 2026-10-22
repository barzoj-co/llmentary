// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"net/url"
	"strings"
	"testing"

	"github.com/barzoj-co/llmentary/internal/schemalint"
	"github.com/barzoj-co/llmentary/internal/specvalidate"
)

func TestDefaultSpecTargetHosts(t *testing.T) {
	t.Parallel()
	cases := []struct {
		raw  string
		want string
	}{
		{"https://api.openai.com/v1", SpecTargetOpenAI},
		{"https://eastus.openai.azure.com", SpecTargetOpenAI},
		{"https://res.cognitiveservices.azure.com", SpecTargetOpenAI},
		{"https://res.services.ai.azure.com", SpecTargetOpenAI},
		{"http://127.0.0.1:11434/v1", SpecTargetCompatible},
		{"http://localhost:11434/v1", SpecTargetCompatible},
		{"https://openrouter.ai/api/v1", SpecTargetCompatible},
		{"https://api.deepseek.com", SpecTargetCompatible},
		{"https://generativelanguage.googleapis.com/v1beta/openai", SpecTargetCompatible},
	}
	for _, tc := range cases {
		u, err := url.Parse(tc.raw)
		if err != nil {
			t.Fatal(err)
		}
		if got := defaultSpecTarget(u); got != tc.want {
			t.Fatalf("%s: got %s want %s", tc.raw, got, tc.want)
		}
	}
	if defaultSpecTarget(nil) != SpecTargetCompatible {
		t.Fatal("nil url")
	}
}

func TestAddSpecTargetAndValidate(t *testing.T) {
	cfg := Config{ListenAddr: "127.0.0.1:1", ControlAddr: "127.0.0.1:2"}
	if err := cfg.AddUpstream("openai=https://api.openai.com/v1"); err != nil {
		t.Fatal(err)
	}
	if err := cfg.AddUpstream("ollama=http://127.0.0.1:11434/v1"); err != nil {
		t.Fatal(err)
	}
	if err := cfg.AddSpecTarget("ollama=openai-compatible"); err != nil {
		t.Fatal(err)
	}
	if err := cfg.AddSpecTarget("   "); err != nil {
		t.Fatal(err)
	}
	if err := cfg.AddSpecTarget("openai-compatible"); err == nil {
		t.Fatal("missing name")
	}
	if err := cfg.AddSpecTarget("=openai"); err == nil {
		t.Fatal("empty name")
	}
	if err := cfg.AddSpecTarget("ollama=strict"); err == nil || !strings.Contains(err.Error(), "allowed values") {
		t.Fatalf("invalid value: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.specTargetFor("openai", cfg.Upstreams["openai"]) != SpecTargetOpenAI {
		t.Fatal("openai default")
	}
	if cfg.specTargetFor("ollama", cfg.Upstreams["ollama"]) != SpecTargetCompatible {
		t.Fatal("explicit ollama")
	}
	resolved := cfg.resolvedSpecTargets()
	if resolved["openai"] != SpecTargetOpenAI || resolved["ollama"] != SpecTargetCompatible {
		t.Fatalf("resolved=%v", resolved)
	}

	cfg.SpecTargets["missing"] = SpecTargetOpenAI
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "unknown upstream") {
		t.Fatalf("unknown name: %v", err)
	}
}

func TestConfigFromEnvSpecTargets(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("LLMENTARY_UPSTREAMS", "openai=https://api.openai.com/v1,ds=https://api.deepseek.com")
	t.Setenv("LLMENTARY_SPEC_TARGETS", "ds=openai-compatible,openai=openai")
	cfg, err := ConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SpecTargets["ds"] != SpecTargetCompatible || cfg.SpecTargets["openai"] != SpecTargetOpenAI {
		t.Fatalf("targets=%v", cfg.SpecTargets)
	}
	clearConfigEnv(t)
	t.Setenv("LLMENTARY_SPEC_TARGETS", "ds=nope")
	if _, err := ConfigFromEnv(); err == nil || !strings.Contains(err.Error(), "allowed values") {
		t.Fatalf("bad env: %v", err)
	}
}

func TestApplySpecTargetCompatible(t *testing.T) {
	t.Parallel()
	vs := []specvalidate.Violation{
		{Side: specvalidate.SideRequest, Keyword: "unevaluatedProperties", Pointer: "/top_k", Path: "top_k", Message: "unknown"},
		{Side: specvalidate.SideRequest, Keyword: "enum", Pointer: "/tool_choice", Path: "tool_choice", Message: "bad"},
		{Side: specvalidate.SideRequest, Keyword: "type", Pointer: "/max_tokens", Path: "max_tokens", Message: "got string, want integer"},
		{Side: specvalidate.SideResponse, Keyword: "enum", Pointer: "/choices/0/finish_reason", Path: "choices[0].finish_reason"},
	}
	ig := []schemalint.Finding{{
		Kind: schemalint.KindUnknownRequest, Pointer: "/messages/1/reasoning_content",
		Path: "messages[1].reasoning_content", Key: "reasoning_content",
		Action: schemalint.ActionReject, Severity: schemalint.SeverityWarning,
		Hint: unknownRequestHintOA,
	}}
	gotV, gotI := applySpecTarget(SpecTargetCompatible, vs, ig)
	if reject, kind := shouldReject(gotV, gotI); !reject || kind != "spec_violation" {
		t.Fatalf("type error must still reject: reject=%v kind=%s", reject, kind)
	}
	var hasType, hasEnumWarn bool
	for _, v := range gotV {
		if v.Keyword == "unevaluatedProperties" {
			t.Fatal("vendor property stayed as violation")
		}
		if v.Keyword == "type" && v.Pointer == "/max_tokens" {
			hasType = true
		}
		if v.Keyword == "enum" && v.Pointer == "/tool_choice" {
			if v.Severity != specvalidate.SeverityWarning {
				t.Fatalf("enum severity=%q", v.Severity)
			}
			hasEnumWarn = true
		}
	}
	if !hasType || !hasEnumWarn {
		t.Fatalf("kept=%v", gotV)
	}
	foundVendor := false
	for _, f := range gotI {
		if f.Kind == schemalint.KindVendorField && f.Action == schemalint.ActionReport && f.Severity == schemalint.SeverityInfo {
			foundVendor = true
			if strings.Contains(f.Hint, "silently ignores") {
				t.Fatalf("openai hint leaked: %q", f.Hint)
			}
		}
	}
	if !foundVendor {
		t.Fatalf("ignored=%v", gotI)
	}
}

func TestApplySpecTargetOpenAIKeepsReject(t *testing.T) {
	t.Parallel()
	vs := []specvalidate.Violation{{Side: specvalidate.SideRequest, Keyword: "unevaluatedProperties", Pointer: "/top_k"}}
	ig := []schemalint.Finding{{
		Kind: schemalint.KindUnknownRequest, Action: schemalint.ActionReject,
		Severity: schemalint.SeverityWarning, Hint: unknownRequestHintOA,
	}}
	gotV, gotI := applySpecTarget(SpecTargetOpenAI, vs, ig)
	if reject, kind := shouldReject(gotV, gotI); !reject || kind != "spec_violation" {
		t.Fatalf("openai must reject vendor field: %v %s", reject, kind)
	}
	if gotI[0].Kind != schemalint.KindUnknownRequest || gotI[0].Action != schemalint.ActionReject {
		t.Fatalf("ignored mutated: %+v", gotI[0])
	}
}

func TestIsExtendedEnumPointers(t *testing.T) {
	t.Parallel()
	if !isExtendedEnumViolation(specvalidate.Violation{Keyword: "enum", Pointer: "/response_format/type"}) {
		t.Fatal("response_format.type")
	}
	if !isExtendedEnumViolation(specvalidate.Violation{Keyword: "const", Pointer: "/service_tier"}) {
		t.Fatal("service_tier")
	}
	if isExtendedEnumViolation(specvalidate.Violation{Keyword: "enum", Pointer: "/messages/0/role"}) {
		t.Fatal("role enum is not vendor-extended")
	}
}
