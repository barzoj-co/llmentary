// SPDX-License-Identifier: AGPL-3.0-only

package proxy_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/barzoj-co/llmentary/internal/proxy"
	"github.com/barzoj-co/llmentary/internal/schemalint"
)

func withTarget(target string) func(*proxy.Config) {
	return func(cfg *proxy.Config) {
		cfg.SpecTargets = map[string]string{"default": target}
	}
}

func schemaWithKeyword(keyword string) map[string]any {
	prop := map[string]any{"type": "string"}
	switch keyword {
	case "minLength":
		prop["minLength"] = 1
	case "maxLength":
		prop["maxLength"] = 32
	case "pattern":
		prop["pattern"] = "^[a-z]+$"
	case "format":
		prop["format"] = "uri"
	case "default":
		prop["default"] = "Paris"
	case "examples":
		prop["examples"] = []any{"Paris"}
	case "oneOf":
		prop["oneOf"] = []any{map[string]any{"const": "c"}, map[string]any{"const": "f"}}
	case "allOf":
		prop["allOf"] = []any{map[string]any{"type": "string"}}
	case "not":
		prop["not"] = map[string]any{"type": "integer"}
	case "$ref":
		return map[string]any{
			"type": "object",
			"properties": map[string]any{
				"city": map[string]any{"$ref": "#/$defs/missing"},
			},
			"required":             []any{"city"},
			"additionalProperties": false,
		}
	default:
		prop[keyword] = true
	}
	return map[string]any{
		"type":                 "object",
		"properties":           map[string]any{"city": prop},
		"required":             []any{"city"},
		"additionalProperties": false,
	}
}

func chatToolsBody(model, keyword, strict string) []byte {
	fn := map[string]any{"name": "get_weather", "parameters": schemaWithKeyword(keyword)}
	switch strict {
	case "true":
		fn["strict"] = true
	case "false":
		fn["strict"] = false
	}
	body := map[string]any{
		"model":    model,
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
		"tools":    []any{map[string]any{"type": "function", "function": fn}},
	}
	raw, _ := json.Marshal(body)
	return raw
}

func chatJSONSchemaBody(model, keyword, strict string) []byte {
	js := map[string]any{"name": "weather", "schema": schemaWithKeyword(keyword)}
	switch strict {
	case "true":
		js["strict"] = true
	case "false":
		js["strict"] = false
	}
	body := map[string]any{
		"model":           model,
		"messages":        []any{map[string]any{"role": "user", "content": "hi"}},
		"response_format": map[string]any{"type": "json_schema", "json_schema": js},
	}
	raw, _ := json.Marshal(body)
	return raw
}

func responsesToolsBody(model, keyword, strict string) []byte {
	tool := map[string]any{"type": "function", "name": "get_weather", "parameters": schemaWithKeyword(keyword)}
	switch strict {
	case "true":
		tool["strict"] = true
	case "false":
		tool["strict"] = false
	}
	body := map[string]any{
		"model": model,
		"input": "hi",
		"tools": []any{tool},
	}
	raw, _ := json.Marshal(body)
	return raw
}

func responsesTextFormatBody(model, keyword, strict string) []byte {
	format := map[string]any{"type": "json_schema", "name": "weather", "schema": schemaWithKeyword(keyword)}
	switch strict {
	case "true":
		format["strict"] = true
	case "false":
		format["strict"] = false
	}
	body := map[string]any{
		"model": model,
		"input": "hi",
		"text":  map[string]any{"format": format},
	}
	raw, _ := json.Marshal(body)
	return raw
}

func keywordExpectsFinding(keyword string, fineTuned bool) bool {
	switch keyword {
	case "pattern":
		// Supported on the general subset; reject only for ft: models.
		return fineTuned
	case "default":
		// Walked as a structural key, not an OpenAI-subset lint.
		return false
	case "format", "minLength", "maxLength", "examples", "oneOf", "allOf", "not", "$ref":
		return true
	default:
		return true
	}
}

func TestOpenAIOnlyLintsMatrix(t *testing.T) {
	keywords := []string{"minLength", "maxLength", "pattern", "format", "default", "examples", "oneOf", "allOf", "not", "$ref"}
	surfaces := []struct {
		name, path string
		body       func(model, keyword, strict string) []byte
		ok         bool
	}{
		{"chat_tools", "/v1/chat/completions", chatToolsBody, true},
		{"chat_json_schema", "/v1/chat/completions", chatJSONSchemaBody, true},
		{"resp_tools", "/v1/responses", responsesToolsBody, false},
		{"resp_text_format", "/v1/responses", responsesTextFormatBody, false},
	}
	stricts := []string{"omitted", "false", "true"}
	targets := []string{proxy.SpecTargetOpenAI, proxy.SpecTargetCompatible}

	type proxyKey struct{ target, family string }
	proxies := map[proxyKey]proxyProc{}
	for _, target := range targets {
		proxies[proxyKey{target, "chat"}] = startSpecProxy(t, "enforce", "relaxed", chatOK(), withTarget(target))
		proxies[proxyKey{target, "resp"}] = startSpecProxy(t, "enforce", "relaxed", responsesOK(), withTarget(target))
	}

	for _, kw := range keywords {
		for _, surface := range surfaces {
			for _, strict := range stricts {
				for _, target := range targets {
					name := fmt.Sprintf("%s/%s/strict=%s/%s", kw, surface.name, strict, target)
					t.Run(name, func(t *testing.T) {
						family := "chat"
						if strings.HasPrefix(surface.name, "resp") {
							family = "resp"
						}
						p := proxies[proxyKey{target, family}]
						sid := newSession()
						status, _, _, session := postRaw(t, p, surface.path, sid, nil, surface.body("gpt-fixture", kw, strict))
						n := normalized(lastCall(t, session))
						expectFinding := keywordExpectsFinding(kw, false)
						if target == proxy.SpecTargetCompatible {
							if status != 200 {
								t.Fatalf("compatible enforce status=%d body findings=%v", status, n["ignored_fields"])
							}
							if expectFinding && !ignoredHasKeyword(n, kw) && !ignoredHasKind(n, schemalint.KindUnresolvedRef) && !ignoredHasKind(n, schemalint.KindUnsupportedFormat) {
								t.Fatalf("compatible should still list finding for %s: %v", kw, n["ignored_fields"])
							}
							if expectFinding && !ignoredHasCompatibleLintHint(n) {
								t.Fatalf("compatible rematch missing target-neutral hint: %v", n["ignored_fields"])
							}
							if ignoredHasReject(n) {
								t.Fatalf("compatible finding still reject_in_enforce: %v", n["ignored_fields"])
							}
							return
						}
						if !expectFinding {
							if status != 200 {
								t.Fatalf("openai supported keyword %s rejected: status=%d ignored=%v", kw, status, n["ignored_fields"])
							}
							return
						}
						if status != 400 {
							t.Fatalf("openai enforce status=%d want 400 ignored=%v", status, n["ignored_fields"])
						}
					})
				}
			}
		}
	}
}

func TestOpenAIOnlyLintCorners(t *testing.T) {
	compatible := startSpecProxy(t, "enforce", "relaxed", chatOK(), withTarget(proxy.SpecTargetCompatible))
	openai := startSpecProxy(t, "enforce", "relaxed", chatOK(), withTarget(proxy.SpecTargetOpenAI))

	t.Run("nested_items_and_anyOf", func(t *testing.T) {
		schema := map[string]any{
			"type": "object",
			"properties": map[string]any{
				"tags": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type":      "string",
						"minLength": 1,
						"examples":  []any{"x"},
					},
				},
				"unit": map[string]any{
					"anyOf": []any{
						map[string]any{"type": "string", "default": "c"},
						map[string]any{"type": "string", "not": map[string]any{"const": "k"}},
					},
				},
			},
			"required": []any{"tags"},
		}
		fn := map[string]any{"name": "get_weather", "parameters": schema}
		body, _ := json.Marshal(map[string]any{
			"model":    "gpt-fixture",
			"messages": []any{map[string]any{"role": "user", "content": "hi"}},
			"tools":    []any{map[string]any{"type": "function", "function": fn}},
		})
		sid := newSession()
		status, _, _, session := postRaw(t, compatible, "/v1/chat/completions", sid, nil, body)
		if status != 200 {
			t.Fatalf("status=%d", status)
		}
		n := normalized(lastCall(t, session))
		if !ignoredHasKeyword(n, "minLength") || !ignoredHasKeyword(n, "examples") {
			t.Fatalf("nested findings missing: %v", n["ignored_fields"])
		}
	})

	t.Run("defs_unresolved_and_resolved", func(t *testing.T) {
		unresolved := chatToolsBody("gpt-fixture", "$ref", "omitted")
		sid := newSession()
		status, _, _, session := postRaw(t, compatible, "/v1/chat/completions", sid, nil, unresolved)
		if status != 200 || !ignoredHasKind(normalized(lastCall(t, session)), schemalint.KindUnresolvedRef) {
			t.Fatalf("unresolved $ref status=%d ignored=%v", status, normalized(lastCall(t, session))["ignored_fields"])
		}

		resolvedSchema := map[string]any{
			"type": "object",
			"$defs": map[string]any{
				"city": map[string]any{"type": "string"},
			},
			"properties": map[string]any{
				"city": map[string]any{"$ref": "#/$defs/city"},
			},
			"required": []any{"city"},
		}
		fn := map[string]any{"name": "get_weather", "parameters": resolvedSchema}
		body, _ := json.Marshal(map[string]any{
			"model":    "gpt-fixture",
			"messages": []any{map[string]any{"role": "user", "content": "hi"}},
			"tools":    []any{map[string]any{"type": "function", "function": fn}},
		})
		sid = newSession()
		status, _, _, session = postRaw(t, compatible, "/v1/chat/completions", sid, nil, body)
		if status != 200 {
			t.Fatalf("resolved $ref status=%d", status)
		}
		if ignoredHasKind(normalized(lastCall(t, session)), schemalint.KindUnresolvedRef) {
			t.Fatalf("resolved $ref flagged: %v", normalized(lastCall(t, session))["ignored_fields"])
		}
	})

	t.Run("ft_prefix_pattern", func(t *testing.T) {
		body := chatToolsBody("ft:gpt-fixture:org:demo", "pattern", "omitted")
		sid := newSession()
		status, _, _, session := postRaw(t, openai, "/v1/chat/completions", sid, nil, body)
		if status != 400 {
			t.Fatalf("ft pattern on openai should reject, status=%d ignored=%v", status, normalized(lastCall(t, session))["ignored_fields"])
		}
		sid = newSession()
		status, _, _, session = postRaw(t, compatible, "/v1/chat/completions", sid, nil, body)
		if status != 200 {
			t.Fatalf("ft pattern on compatible should pass, status=%d", status)
		}
	})
}

func TestCompatibleStrictFallbackPredictedInfo(t *testing.T) {
	p := startSpecProxy(t, "enforce", "relaxed", responsesOK(), withTarget(proxy.SpecTargetCompatible))
	body := []byte(`{"model":"gpt-fixture","input":"hi","tools":[{"type":"function","name":"get_weather","parameters":{"type":"object","properties":{"city":{"type":"string","minLength":1}},"required":["city"]}}]}`)
	sid := newSession()
	status, _, _, session := postRaw(t, p, "/v1/responses", sid, nil, body)
	if status != 200 {
		t.Fatalf("status=%d", status)
	}
	n := normalized(lastCall(t, session))
	fb := ignoredFinding(n, schemalint.KindStrictFallback)
	if fb == nil {
		t.Fatalf("expected confirmed strict_fallback: %v", n["ignored_fields"])
	}
	if fb["severity"] != schemalint.SeverityInfo || fb["action"] != schemalint.ActionReport {
		t.Fatalf("strict_fallback not rematched: %v", fb)
	}
	if !strings.Contains(fmt.Sprint(fb["hint"]), "informational for the selected upstream") {
		t.Fatalf("strict_fallback hint=%v", fb["hint"])
	}
	if ignoredHasReject(n) {
		t.Fatalf("compatible fallback still reject: %v", n["ignored_fields"])
	}
}

func TestOpenAIStrictFallbackResponseRetainsDisposition(t *testing.T) {
	p := startSpecProxy(t, "warn", "relaxed", responsesOK(), withTarget(proxy.SpecTargetOpenAI))
	body := []byte(`{"model":"gpt-fixture","input":"hi","tools":[{"type":"function","name":"get_weather","parameters":{"type":"object","properties":{"city":{"type":"string","minLength":1}},"required":["city"]}}]}`)
	sid := newSession()
	status, _, _, session := postRaw(t, p, "/v1/responses", sid, nil, body)
	if status != 200 {
		t.Fatalf("warn openai status=%d", status)
	}
	n := normalized(lastCall(t, session))
	fb := ignoredFinding(n, schemalint.KindStrictFallback)
	if fb == nil {
		t.Fatalf("expected confirmed strict_fallback: %v", n["ignored_fields"])
	}
	if fb["severity"] != schemalint.SeverityWarning || fb["action"] != schemalint.ActionReject {
		t.Fatalf("openai warn must keep reject disposition: %v", fb)
	}
	if strings.Contains(fmt.Sprint(fb["hint"]), "informational for the selected upstream") {
		t.Fatalf("openai hint rematched: %v", fb["hint"])
	}
	pred := ignoredFinding(n, schemalint.KindStrictFallbackPred)
	if pred == nil || pred["action"] != schemalint.ActionReject || pred["severity"] != schemalint.SeverityWarning {
		t.Fatalf("predicted fallback lost reject: %v", pred)
	}
}

func ignoredFinding(n map[string]any, kind string) map[string]any {
	for _, v := range asAnySlice(n["ignored_fields"]) {
		m, _ := v.(map[string]any)
		if m["kind"] == kind {
			return m
		}
	}
	return nil
}

func ignoredHasCompatibleLintHint(n map[string]any) bool {
	for _, v := range asAnySlice(n["ignored_fields"]) {
		m, _ := v.(map[string]any)
		if strings.Contains(fmt.Sprint(m["hint"]), "informational for the selected upstream") {
			return true
		}
	}
	return false
}

func ignoredHasKeyword(n map[string]any, key string) bool {
	for _, v := range asAnySlice(n["ignored_fields"]) {
		m, _ := v.(map[string]any)
		if m["key"] == key {
			return true
		}
	}
	return false
}

func ignoredHasKind(n map[string]any, kind string) bool {
	for _, v := range asAnySlice(n["ignored_fields"]) {
		m, _ := v.(map[string]any)
		if m["kind"] == kind {
			return true
		}
	}
	return false
}

func ignoredHasReject(n map[string]any) bool {
	for _, v := range asAnySlice(n["ignored_fields"]) {
		m, _ := v.(map[string]any)
		if m["action"] == schemalint.ActionReject && m["severity"] != schemalint.SeverityInfo {
			return true
		}
	}
	return false
}
