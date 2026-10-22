// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"net/url"
	"os"
	"path/filepath"
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
	gotV, gotI := applySpecTarget(SpecTargetCompatible, vs, ig, nil)
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

func TestApplySpecTargetCompatibleDowngradesOpenAILints(t *testing.T) {
	t.Parallel()
	ig := []schemalint.Finding{
		{Kind: schemalint.KindUnknownKeyword, Key: "minLength", Action: schemalint.ActionReject, Severity: schemalint.SeverityWarning},
		{Kind: schemalint.KindUnsupportedKeyword, Key: "allOf", Action: schemalint.ActionReject, Severity: schemalint.SeverityWarning},
		{Kind: schemalint.KindStrictFallbackPred, Key: "strict", Action: schemalint.ActionReject, Severity: schemalint.SeverityWarning},
		{Kind: schemalint.KindUnresolvedRef, Key: "$ref", Action: schemalint.ActionReject, Severity: schemalint.SeverityWarning},
	}
	_, got := applySpecTarget(SpecTargetCompatible, nil, ig, nil)
	if reject, kind := shouldReject(nil, got); reject {
		t.Fatalf("compatible must not reject openai-only lints: kind=%s", kind)
	}
	for _, f := range got {
		if f.Severity != schemalint.SeverityInfo || f.Action != schemalint.ActionReport {
			t.Fatalf("not rematched: %+v", f)
		}
		if f.Hint != compatibleLintHint {
			t.Fatalf("hint=%q want target-neutral", f.Hint)
		}
	}
}

func TestApplySpecTargetCompatibleHintCorners(t *testing.T) {
	t.Parallel()
	openaiHint := "OpenAI does not guarantee minLength."
	ig := []schemalint.Finding{
		{Kind: schemalint.KindUnknownKeyword, Key: "minLength", Action: schemalint.ActionReject, Severity: schemalint.SeverityWarning, Hint: openaiHint},
		{Kind: schemalint.KindUnknownRequest, Key: "top_k", Action: schemalint.ActionReject, Severity: schemalint.SeverityWarning, Hint: unknownRequestHintOA},
		{Kind: schemalint.KindStrictFallback, Key: "strict", Action: schemalint.ActionReject, Severity: schemalint.SeverityWarning, Hint: openaiHint},
	}
	_, got := applySpecTarget(SpecTargetCompatible, nil, ig, nil)
	var sawKeyword, sawVendor, sawFallback bool
	for _, f := range got {
		switch f.Kind {
		case schemalint.KindUnknownKeyword:
			sawKeyword = true
			if f.Hint != compatibleLintHint {
				t.Fatalf("keyword hint leaked OpenAI wording: %q", f.Hint)
			}
		case schemalint.KindVendorField:
			sawVendor = true
			if f.Hint != vendorFieldHint {
				t.Fatalf("vendor hint=%q", f.Hint)
			}
			if f.Hint == compatibleLintHint {
				t.Fatal("vendor rematch used lint hint")
			}
		case schemalint.KindStrictFallback:
			sawFallback = true
			if f.Hint != compatibleLintHint || f.Action != schemalint.ActionReport || f.Severity != schemalint.SeverityInfo {
				t.Fatalf("fallback rematch: %+v", f)
			}
		default:
			t.Fatalf("unexpected kind %s", f.Kind)
		}
	}
	if !sawKeyword || !sawVendor || !sawFallback {
		t.Fatalf("missing rematch kinds: %+v", got)
	}
}

func TestApplySpecTargetOpenAIKeepsKeywordReject(t *testing.T) {
	t.Parallel()
	ig := []schemalint.Finding{{
		Kind: schemalint.KindUnknownKeyword, Key: "examples",
		Action: schemalint.ActionReject, Severity: schemalint.SeverityWarning,
	}}
	_, got := applySpecTarget(SpecTargetOpenAI, nil, ig, nil)
	if reject, kind := shouldReject(nil, got); !reject || kind != "ignored_field" {
		t.Fatalf("openai must keep reject: reject=%v kind=%s", reject, kind)
	}
	if got[0].Action != schemalint.ActionReject || got[0].Severity != schemalint.SeverityWarning {
		t.Fatalf("openai lint mutated: %+v", got[0])
	}
	if got[0].Hint == compatibleLintHint {
		t.Fatal("openai target must keep the OpenAI lint hint")
	}
}

func TestApplySpecTargetOpenAIKeepsExistingLintHint(t *testing.T) {
	t.Parallel()
	hint := "OpenAI does not guarantee `examples`."
	ig := []schemalint.Finding{{
		Kind: schemalint.KindUnknownKeyword, Key: "examples",
		Action: schemalint.ActionReject, Severity: schemalint.SeverityWarning, Hint: hint,
	}}
	_, got := applySpecTarget(SpecTargetOpenAI, nil, ig, nil)
	if got[0].Hint != hint {
		t.Fatalf("openai hint overwritten: %q", got[0].Hint)
	}
}

func TestApplySpecTargetOpenAIKeepsReject(t *testing.T) {
	t.Parallel()
	vs := []specvalidate.Violation{{Side: specvalidate.SideRequest, Keyword: "unevaluatedProperties", Pointer: "/top_k"}}
	ig := []schemalint.Finding{{
		Kind: schemalint.KindUnknownRequest, Action: schemalint.ActionReject,
		Severity: schemalint.SeverityWarning, Hint: unknownRequestHintOA,
	}}
	gotV, gotI := applySpecTarget(SpecTargetOpenAI, vs, ig, nil)
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

func TestCompatibleRejectsKnownFieldTypeEnumRequired(t *testing.T) {
	t.Parallel()
	reject := []specvalidate.Violation{
		{Side: specvalidate.SideRequest, Keyword: "type", Pointer: "/max_completion_tokens", Message: "got string"},
		{Side: specvalidate.SideRequest, Keyword: "type", Pointer: "/presence_penalty"},
		{Side: specvalidate.SideRequest, Keyword: "type", Pointer: "/messages/0/content", Message: "got number, want string"},
		{Side: specvalidate.SideRequest, Keyword: "type", Pointer: "/messages/0/role", Message: "got number, want string"},
		{Side: specvalidate.SideRequest, Keyword: "required", Pointer: "/messages/0", Message: "missing property 'content'"},
	}
	for _, v := range reject {
		if !isCompatibleReject(v, nil) {
			t.Fatalf("should reject %+v", v)
		}
	}
	gotV, _ := applyCompatibleTarget(reject, nil, nil)
	if ok, kind := shouldReject(gotV, nil); !ok || kind != "spec_violation" {
		t.Fatalf("reject=%v kind=%s kept=%v", ok, kind, gotV)
	}
	leftovers := []specvalidate.Violation{
		{Side: specvalidate.SideRequest, Keyword: "required", Pointer: "/tools/0", Message: "missing property 'type'"},
		{Side: specvalidate.SideRequest, Keyword: "required", Pointer: "/messages/0", Message: "missing property 'tool_call_id'"},
		{Side: specvalidate.SideRequest, Keyword: "required", Pointer: "/response_format", Message: "missing property 'json_schema'"},
		{Side: specvalidate.SideRequest, Keyword: "type", Pointer: "/tool_choice", Message: "got string, want object"},
		{Side: specvalidate.SideRequest, Keyword: "type", Pointer: "/stream_options", Message: "got object, want null"},
		{Side: specvalidate.SideRequest, Keyword: "type", Pointer: "/reasoning", Message: "got object, want null"},
		{Side: specvalidate.SideRequest, Keyword: "type", Pointer: "/input/0/content", Message: "got array, want string"},
		{Side: specvalidate.SideRequest, Keyword: "required", Pointer: "/input/0/content/0", Message: "missing property 'type'"},
		{Side: specvalidate.SideRequest, Keyword: "type", Pointer: "/messages/0/content", Message: "got null, want string"},
		{Side: specvalidate.SideRequest, Keyword: "type", Pointer: "/messages/0/content", Message: "got array, want string"},
		{Side: specvalidate.SideRequest, Keyword: "enum", Pointer: "/messages/0/role", Message: "value must be 'tool'"},
		{Side: specvalidate.SideRequest, Keyword: "enum", Pointer: "/input/0/type", Message: "value must be 'message'"},
	}
	for _, leftover := range leftovers {
		if isCompatibleReject(leftover, nil) {
			t.Fatalf("leftover must not reject: %+v", leftover)
		}
	}
	gotV, _ = applyCompatibleTarget(leftovers, nil, nil)
	if ok, _ := shouldReject(gotV, nil); ok {
		t.Fatalf("leftover rejected: %v", gotV)
	}
	if gotV[0].Severity != specvalidate.SeverityWarning {
		t.Fatalf("leftover severity=%q", gotV[0].Severity)
	}
	extended := specvalidate.Violation{Side: specvalidate.SideRequest, Keyword: "enum", Pointer: "/tool_choice"}
	if isCompatibleReject(extended, nil) {
		t.Fatal("extended enum must stay warning")
	}
}

func TestCompatibleRejectsExhaustedRoleEnum(t *testing.T) {
	t.Parallel()
	losing := specvalidate.Violation{Side: specvalidate.SideRequest, Keyword: "enum", Pointer: "/messages/0/role", Message: "value must be 'tool'"}
	if isCompatibleReject(losing, nil) {
		t.Fatal("single losing-branch role enum is leftover")
	}
	gotV, _ := applyCompatibleTarget([]specvalidate.Violation{losing}, nil, nil)
	if ok, _ := shouldReject(gotV, nil); ok {
		t.Fatal("single losing-branch role enum rejected")
	}
	var all []specvalidate.Violation
	for _, role := range chatRoleEnumValues {
		all = append(all, specvalidate.Violation{
			Side: specvalidate.SideRequest, Keyword: "enum", Pointer: "/messages/0/role",
			Message: "value must be '" + role + "'",
		})
	}
	gotV, _ = applyCompatibleTarget(all, nil, nil)
	if ok, kind := shouldReject(gotV, nil); !ok || kind != "spec_violation" {
		t.Fatalf("exhausted role enum must reject: reject=%v kind=%s kept=%v", ok, kind, gotV)
	}
}

func TestCompatibleKnownRequiredCorners(t *testing.T) {
	t.Parallel()
	if !isKnownFieldRequiredViolation(specvalidate.Violation{Pointer: "/messages/1", Message: "missing property 'role'"}, nil) {
		t.Fatal("chat missing role")
	}
	if isKnownFieldRequiredViolation(specvalidate.Violation{Pointer: "/input/0", Message: "missing property 'role'"}, nil) {
		t.Fatal("responses item missing role is leftover")
	}
	if isKnownFieldRequiredViolation(specvalidate.Violation{Pointer: "/", Message: "missing property 'tools'"}, nil) {
		t.Fatal("root tools is not core")
	}
	if !isKnownFieldRequiredViolation(specvalidate.Violation{Pointer: "/", Message: "missing property 'messages'"}, nil) {
		t.Fatal("root messages")
	}
	if isKnownFieldTypeViolation(specvalidate.Violation{Pointer: "/input", Message: "got string, want array"}) ||
		isKnownFieldTypeViolation(specvalidate.Violation{Pointer: "/response_format", Message: "got object"}) {
		t.Fatal("union objects are not known scalars")
	}
	if !isKnownFieldTypeViolation(specvalidate.Violation{Pointer: "/messages", Message: "got string, want array"}) {
		t.Fatal("messages type")
	}
	if isKnownFieldTypeViolation(specvalidate.Violation{Pointer: "/messages/2/content", Message: "got null, want string"}) {
		t.Fatal("null content is leftover")
	}
	if !isKnownFieldTypeViolation(specvalidate.Violation{Pointer: "/messages/2/content", Message: "got number, want string"}) {
		t.Fatal("numeric content")
	}
	asst := requestInstance([]byte(`{"messages":[{"role":"assistant","tool_calls":[{"id":"c1"}],"reasoning_content":"t"}]}`))
	contentMiss := specvalidate.Violation{Keyword: "required", Pointer: "/messages/0", Message: "missing property 'content'"}
	if isKnownFieldRequiredViolation(contentMiss, asst) || isCompatibleReject(contentMiss, asst) {
		t.Fatal("assistant tool-call missing content is leftover")
	}
	user := requestInstance([]byte(`{"messages":[{"role":"user"}]}`))
	if !isKnownFieldRequiredViolation(contentMiss, user) || !isCompatibleReject(contentMiss, user) {
		t.Fatal("user missing content must still reject")
	}
	if isAssistantContentLeftover(contentMiss, nil) || messageAtPointer(nil, "/messages/0") != nil {
		t.Fatal("nil instance corners")
	}
}

func TestRejectClassExamplesOmitDefaultKeyword(t *testing.T) {
	t.Parallel()
	files := []string{
		filepath.Join("..", "..", "api", "openai", "schema-subset.yaml"),
		filepath.Join("..", "..", "internal", "specvalidate", "embed", "schema-subset.yaml"),
		filepath.Join("..", "..", "docs", "providers.md"),
		filepath.Join("..", "..", "docs", "proxy.md"),
	}
	for _, rel := range files {
		raw, err := os.ReadFile(rel)
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		if strings.Contains(text, "oneOf, default") || strings.Contains(text, "`oneOf`, `default`") {
			t.Fatalf("%s still lists default as a reject-class example", rel)
		}
		if !strings.Contains(text, "oneOf") {
			t.Fatalf("%s lost the reject-class keyword examples", rel)
		}
	}
}
