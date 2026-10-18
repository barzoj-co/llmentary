// SPDX-License-Identifier: AGPL-3.0-only

//go:build e2e

package llmtestlab_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// tokenDeltaEpsilon absorbs serialization artifacts of an extra JSON key.
// A real reading of T would cost hundreds of tokens, so ε is not 0.
const tokenDeltaEpsilon = 2

const tokenDeltaMinControl = 200

func TestLiveTokens_UnknownInMessage_Chat(t *testing.T) {
	T := loadTokenBlock(t)
	model := liveModel()
	base := map[string]any{
		"model":      model,
		"max_tokens": 16,
		"messages":   []any{map[string]any{"role": "user", "content": "Reply with ok."}},
	}
	with := cloneMap(base)
	with["messages"] = []any{map[string]any{"role": "user", "content": "Reply with ok.", "foo": T}}
	ctrl := cloneMap(base)
	ctrl["messages"] = []any{map[string]any{"role": "user", "content": "Reply with ok. " + T}}
	runTokenDelta(t, "/chat/completions", "prompt_tokens", base, with, ctrl, "messages[0].foo", true)
}

func TestLiveTokens_UnknownInFunctionTool_Chat(t *testing.T) {
	T := loadTokenBlock(t)
	model := liveModel()
	tool := map[string]any{"type": "function", "function": map[string]any{"name": "echo", "description": "Echo", "parameters": map[string]any{"type": "object", "properties": map[string]any{}}}}
	base := map[string]any{"model": model, "max_tokens": 16, "messages": []any{map[string]any{"role": "user", "content": "Reply with ok."}}, "tools": []any{tool}}
	withTool := cloneMap(tool)
	fn := cloneMap(tool["function"].(map[string]any))
	fn["foo"] = T
	withTool["function"] = fn
	ctrlTool := cloneMap(tool)
	fn2 := cloneMap(tool["function"].(map[string]any))
	fn2["description"] = "Echo " + T
	ctrlTool["function"] = fn2
	with := cloneMap(base)
	with["tools"] = []any{withTool}
	ctrl := cloneMap(base)
	ctrl["tools"] = []any{ctrlTool}
	runTokenDelta(t, "/chat/completions", "prompt_tokens", base, with, ctrl, "tools[0].function.foo", true)
}

func TestLiveTokens_UnknownInFunctionTool_Responses(t *testing.T) {
	T := loadTokenBlock(t)
	model := liveModel()
	tool := map[string]any{"type": "function", "name": "echo", "description": "Echo", "parameters": map[string]any{"type": "object", "properties": map[string]any{}}}
	base := map[string]any{"model": model, "max_output_tokens": 16, "input": "Reply with ok.", "tools": []any{tool}}
	withTool := cloneMap(tool)
	withTool["foo"] = T
	ctrlTool := cloneMap(tool)
	ctrlTool["description"] = "Echo " + T
	with := cloneMap(base)
	with["tools"] = []any{withTool}
	ctrl := cloneMap(base)
	ctrl["tools"] = []any{ctrlTool}
	runTokenDelta(t, "/responses", "input_tokens", base, with, ctrl, "tools[0].foo", true)
}

func TestLiveTokens_EnumDescriptions_ChatTool(t *testing.T) {
	runEnumDescTokens(t, "/chat/completions", "prompt_tokens", false)
}

func TestLiveTokens_EnumDescriptions_ChatToolStrict(t *testing.T) {
	runEnumDescTokens(t, "/chat/completions", "prompt_tokens", true)
}

func TestLiveTokens_EnumDescriptions_ResponsesTool(t *testing.T) {
	runEnumDescTokens(t, "/responses", "input_tokens", false)
}

func TestLiveTokens_EnumDescriptions_ResponsesToolStrict(t *testing.T) {
	runEnumDescTokens(t, "/responses", "input_tokens", true)
}

func runEnumDescTokens(t *testing.T, path, usageKey string, strict bool) {
	t.Helper()
	T := loadTokenBlock(t)
	descs := enumDescriptionMap(T)
	schema := map[string]any{
		"type":                 "object",
		"properties":           map[string]any{"color": map[string]any{"type": "string", "enum": enumKeys(descs), "description": "Shirt color"}},
		"required":             []any{"color"},
		"additionalProperties": false,
	}
	withSchema := cloneMap(schema)
	props := cloneMap(schema["properties"].(map[string]any))
	color := cloneMap(props["color"].(map[string]any))
	color["enumDescriptions"] = descs
	props["color"] = color
	withSchema["properties"] = props
	ctrlSchema := cloneMap(schema)
	props2 := cloneMap(schema["properties"].(map[string]any))
	color2 := cloneMap(props2["color"].(map[string]any))
	color2["description"] = "Shirt color. " + T
	props2["color"] = color2
	ctrlSchema["properties"] = props2

	model := liveModel()
	var base, with, ctrl map[string]any
	if path == "/chat/completions" {
		fn := map[string]any{"name": "shirt", "parameters": schema}
		if strict {
			fn["strict"] = true
		}
		base = map[string]any{"model": model, "max_tokens": 16, "messages": []any{map[string]any{"role": "user", "content": "Reply with ok."}}, "tool_choice": map[string]any{"type": "function", "function": map[string]any{"name": "shirt"}}, "tools": []any{map[string]any{"type": "function", "function": fn}}}
		fnW := cloneMap(fn)
		fnW["parameters"] = withSchema
		fnC := cloneMap(fn)
		fnC["parameters"] = ctrlSchema
		with = cloneMap(base)
		with["tools"] = []any{map[string]any{"type": "function", "function": fnW}}
		ctrl = cloneMap(base)
		ctrl["tools"] = []any{map[string]any{"type": "function", "function": fnC}}
	} else {
		tool := map[string]any{"type": "function", "name": "shirt", "parameters": schema, "strict": strict}
		base = map[string]any{"model": model, "max_output_tokens": 16, "input": "Reply with ok.", "tool_choice": map[string]any{"type": "function", "name": "shirt"}, "tools": []any{tool}}
		tw := cloneMap(tool)
		tw["parameters"] = withSchema
		tc := cloneMap(tool)
		tc["parameters"] = ctrlSchema
		with = cloneMap(base)
		with["tools"] = []any{tw}
		ctrl = cloneMap(base)
		ctrl["tools"] = []any{tc}
	}
	runTokenDelta(t, path, usageKey, base, with, ctrl, "enumDescriptions", true)
}

func runTokenDelta(t *testing.T, path, usageKey string, base, with, ctrl map[string]any, field string, expectIgnored bool) {
	t.Helper()
	apiKey := requireAPIKey(t)
	p := startLiveProxy(t, "warn", "relaxed")
	b1 := mustJSON(t, base)
	b2 := mustJSON(t, with)
	b3 := mustJSON(t, ctrl)
	s1, body1, n1 := liveUsageCall(t, apiKey, p, path, b1)
	s2, body2, n2 := liveUsageCall(t, apiKey, p, path, b2)
	s3, body3, n3 := liveUsageCall(t, apiKey, p, path, b3)
	if s1 != 200 || s2 != 200 || s3 != 200 {
		if s2 != 200 {
			t.Fatalf("OpenAI now processes %s (delta=n/a / status=%d). Revisit api/openai/schema-subset.yaml, the ignored_fields classification, the hint text, and live-behavior.json. body=%s", field, s2, redactLiveBody(body2))
		}
		t.Fatalf("token-delta HTTP statuses base=%d with=%d control=%d bodies=%s %s %s", s1, s2, s3, redactLiveBody(body1), redactLiveBody(body2), redactLiveBody(body3))
	}
	p1 := usageTokens(t, body1, usageKey)
	p2 := usageTokens(t, body2, usageKey)
	p3 := usageTokens(t, body3, usageKey)
	d2 := p2 - p1
	d3 := p3 - p1
	t.Logf("token_delta field=%s model=%s base=%d with=%d control=%d d2=%d d3=%d", field, liveModel(), p1, p2, p3, d2, d3)
	if abs(d2) > tokenDeltaEpsilon {
		t.Fatalf("OpenAI now processes %s (delta=%d tokens / status=%d). Revisit api/openai/schema-subset.yaml, the ignored_fields classification, the hint text, and live-behavior.json.", field, d2, s2)
	}
	if d3 < tokenDeltaMinControl || d3 < 50*tokenDeltaEpsilon {
		t.Fatalf("measurement method broken: control delta=%d (want ≥ %d and ≥ 50ε). field=%s", d3, tokenDeltaMinControl, field)
	}
	if expectIgnored {
		if !hasIgnoredKey(n2) && field == "enumDescriptions" {
			t.Fatalf("request 2 missing ignored_fields for %s: %v", field, n2["ignored_fields"])
		}
		if field != "enumDescriptions" && !hasIgnoredKind(n2, "unknown_request_field") {
			t.Fatalf("request 2 missing unknown_request_field: %v", n2["ignored_fields"])
		}
		if hasIgnoredKey(n1) || hasIgnoredKey(n3) {
			if field == "enumDescriptions" {
				t.Fatalf("requests 1/3 should have no enumDescriptions ignored_fields")
			}
		}
	}
}

func liveUsageCall(t *testing.T, apiKey string, p liveProxy, path string, body []byte) (int, []byte, map[string]any) {
	t.Helper()
	sid := liveSession()
	status, _, resp, session := postLive(t, apiKey, p, path, sid, body)
	return status, resp, liveNormalized(liveLastCall(t, session))
}

func usageTokens(t *testing.T, body []byte, key string) int {
	t.Helper()
	var env map[string]any
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatal(err)
	}
	usage, _ := env["usage"].(map[string]any)
	if usage == nil {
		t.Fatalf("usage missing in %s", redactLiveBody(body))
	}
	switch v := usage[key].(type) {
	case float64:
		return int(v)
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	default:
		t.Fatalf("usage.%s=%v", key, usage[key])
		return 0
	}
}

func enumDescriptionMap(T string) map[string]any {
	lines := strings.Split(T, "\n")
	out := map[string]any{}
	i := 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		out[fmt.Sprintf("v%02d", i)] = line
		i++
	}
	return out
}

func enumKeys(m map[string]any) []any {
	keys := make([]any, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func cloneMap(in map[string]any) map[string]any {
	raw, _ := json.Marshal(in)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func hasIgnoredKey(n map[string]any) bool {
	for _, v := range liveSlice(n["ignored_fields"]) {
		if v.(map[string]any)["key"] == "enumDescriptions" {
			return true
		}
	}
	return false
}

func hasIgnoredKind(n map[string]any, kind string) bool {
	for _, v := range liveSlice(n["ignored_fields"]) {
		if v.(map[string]any)["kind"] == kind {
			return true
		}
	}
	return false
}
