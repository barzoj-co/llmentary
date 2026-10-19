// SPDX-License-Identifier: AGPL-3.0-only

package schemalint

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/barzoj-co/llmentary/internal/specvalidate"
)

func TestE2DefinitionsLegacy(t *testing.T) {
	l := testLinter(t)
	schema := map[string]any{
		"type": "object",
		"definitions": map[string]any{
			"step": map[string]any{"type": "string", "enumDescriptions": map[string]any{"a": "b"}},
		},
		"properties": map[string]any{
			"one":   map[string]any{"$ref": "#/definitions/step"},
			"two":   map[string]any{"$ref": "#/definitions/step"},
			"three": map[string]any{"$ref": "#/definitions/step"},
		},
	}
	findings := l.lintSchema(schema, "/tools/0/function/parameters", "", ContextChatToolParams, false, false, nil, map[string]struct{}{})
	n := 0
	for _, f := range findings {
		if f.Key == "enumDescriptions" {
			n++
			if !strings.Contains(f.Pointer, "definitions/step") {
				t.Fatalf("expected definition site, got %s", f.Pointer)
			}
		}
	}
	if n != 1 {
		t.Fatalf("want 1 finding at definitions site, got %d: %+v", n, findings)
	}
}

func TestE3MutualRecursion(t *testing.T) {
	l := testLinter(t)
	schema := map[string]any{
		"type": "object",
		"$defs": map[string]any{
			"a": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"to_b": map[string]any{"$ref": "#/$defs/b"},
				},
				"x-a": true,
			},
			"b": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"to_a": map[string]any{"$ref": "#/$defs/a"},
				},
				"x-b": true,
			},
		},
		"properties": map[string]any{
			"start": map[string]any{"$ref": "#/$defs/a"},
		},
	}
	findings := l.lintSchema(schema, "/p", "", ContextChatToolParams, false, false, nil, map[string]struct{}{})
	var a, b int
	for _, f := range findings {
		switch f.Key {
		case "x-a":
			a++
		case "x-b":
			b++
		}
	}
	if a != 1 || b != 1 {
		t.Fatalf("mutual recursion findings a=%d b=%d all=%+v", a, b, findings)
	}
}

func TestE4AnyOfBranchesAndNullPattern(t *testing.T) {
	l := testLinter(t)
	t.Run("branches_linted", func(t *testing.T) {
		schema := map[string]any{
			"anyOf": []any{
				map[string]any{"type": "string"},
				map[string]any{"type": "object", "x-foo": 1, "enumDescriptions": map[string]any{"a": "b"}},
			},
		}
		fs := l.lintSchema(schema, "/tools/0/function/parameters", "", ContextChatToolParams, false, false, nil, map[string]struct{}{})
		var foo, enumDesc bool
		for _, f := range fs {
			if f.Key == "x-foo" {
				foo = true
			}
			if f.Key == "enumDescriptions" {
				enumDesc = true
			}
		}
		if !foo || !enumDesc {
			t.Fatalf("anyOf branches not linted: %+v", fs)
		}
	})
	t.Run("root_anyOf_strict", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{
			"model":    "m",
			"messages": []any{map[string]any{"role": "user", "content": "hi"}},
			"tools": []any{map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":   "x",
					"strict": true,
					"parameters": map[string]any{
						"anyOf": []any{
							map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{"type": "string"}}, "additionalProperties": false, "required": []any{"a"}},
						},
					},
				},
			}},
		})
		res := l.LintRequest("openai.chat", body)
		found := false
		for _, v := range res.StrictViolations {
			if v.Keyword == "anyOf" && v.Source == specvalidate.SourceStrictMode {
				found = true
			}
		}
		if !found {
			t.Fatalf("strict_mode anyOf missing: %+v", res.StrictViolations)
		}
	})
	t.Run("null_pattern_no_finding", func(t *testing.T) {
		schema := map[string]any{
			"anyOf": []any{
				map[string]any{"type": "string"},
				map[string]any{"type": "null"},
			},
		}
		fs := l.lintSchema(schema, "/p", "", ContextChatToolParams, false, false, nil, map[string]struct{}{})
		if len(fs) != 0 {
			t.Fatalf("anyOf null pattern findings=%+v", fs)
		}
	})
}

func TestE5AdditionalPropertiesWalked(t *testing.T) {
	l := testLinter(t)
	t.Run("schema_walked", func(t *testing.T) {
		schema := map[string]any{
			"type": "object",
			"additionalProperties": map[string]any{
				"type":             "string",
				"enumDescriptions": map[string]any{"a": "b"},
			},
		}
		fs := l.lintSchema(schema, "/p", "", ContextChatToolParams, false, false, nil, map[string]struct{}{})
		found := false
		for _, f := range fs {
			if f.Key == "enumDescriptions" && strings.Contains(f.Pointer, "additionalProperties") {
				found = true
			}
		}
		if !found {
			t.Fatalf("additionalProperties schema not walked: %+v", fs)
		}
	})
	t.Run("missing_under_strict", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{
			"model":    "m",
			"messages": []any{map[string]any{"role": "user", "content": "hi"}},
			"tools": []any{map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":   "x",
					"strict": true,
					"parameters": map[string]any{
						"type":       "object",
						"properties": map[string]any{"a": map[string]any{"type": "string"}},
						"required":   []any{"a"},
					},
				},
			}},
		})
		res := l.LintRequest("openai.chat", body)
		found := false
		for _, v := range res.StrictViolations {
			if v.Keyword == "additionalProperties" && v.Source == specvalidate.SourceStrictMode {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing additionalProperties: %+v", res.StrictViolations)
		}
	})
	t.Run("omitted_strict_responses", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{
			"model": "m",
			"input": "hi",
			"tools": []any{map[string]any{
				"type": "function",
				"name": "x",
				"parameters": map[string]any{
					"type":       "object",
					"properties": map[string]any{"a": map[string]any{"type": "string"}},
				},
			}},
		})
		res := l.LintRequest("openai.responses", body)
		found := false
		for _, f := range res.Ignored {
			if f.Kind == KindStrictFallbackPred {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected strict_fallback_predicted: %+v", res.Ignored)
		}
	})
}

func TestE6Limits(t *testing.T) {
	l := testLinter(t)
	lintStrict := func(schema map[string]any) []specvalidate.Violation {
		body, _ := json.Marshal(map[string]any{
			"model":    "m",
			"messages": []any{map[string]any{"role": "user", "content": "hi"}},
			"tools": []any{map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":       "x",
					"strict":     true,
					"parameters": schema,
				},
			}},
		})
		return l.LintRequest("openai.chat", body).StrictViolations
	}
	has := func(vs []specvalidate.Violation, keyword string) bool {
		for _, v := range vs {
			if v.Keyword == keyword {
				return true
			}
		}
		return false
	}

	t.Run("nesting_10_ok", func(t *testing.T) {
		vs := lintStrict(nestedObject(10))
		if has(vs, "maxNesting") {
			t.Fatalf("depth 10 should be ok: %+v", vs)
		}
	})
	t.Run("nesting_11_flagged", func(t *testing.T) {
		vs := lintStrict(nestedObject(11))
		if !has(vs, "maxNesting") {
			t.Fatalf("depth 11 should flag maxNesting: %+v", vs)
		}
	})
	t.Run("properties_5000_ok", func(t *testing.T) {
		vs := lintStrict(nProperties(5000))
		if has(vs, "maxProperties") {
			t.Fatalf("5000 properties should be ok: %+v", vs)
		}
	})
	t.Run("properties_5001_flagged", func(t *testing.T) {
		vs := lintStrict(nProperties(5001))
		if !has(vs, "maxProperties") {
			t.Fatalf("5001 properties should flag maxProperties: %+v", vs)
		}
	})
	t.Run("enum_1000_ok", func(t *testing.T) {
		vs := lintStrict(enumSchema(1000, 1))
		if has(vs, "maxEnumValues") {
			t.Fatalf("1000 enum values should be ok: %+v", vs)
		}
	})
	t.Run("enum_1001_flagged", func(t *testing.T) {
		vs := lintStrict(enumSchema(1001, 1))
		if !has(vs, "maxEnumValues") {
			t.Fatalf("1001 enum values should flag maxEnumValues: %+v", vs)
		}
	})
	t.Run("name_chars", func(t *testing.T) {
		ok := map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties":           map[string]any{strings.Repeat("n", 120000): map[string]any{"type": "string"}},
			"required":             []any{strings.Repeat("n", 120000)},
		}
		if has(lintStrict(ok), "maxNameChars") {
			t.Fatalf("120000 name chars should be ok")
		}
		bad := map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties":           map[string]any{strings.Repeat("n", 120001): map[string]any{"type": "string"}},
			"required":             []any{strings.Repeat("n", 120001)},
		}
		if !has(lintStrict(bad), "maxNameChars") {
			t.Fatalf("120001 name chars should flag maxNameChars")
		}
	})
	t.Run("string_enum_251_over_15000", func(t *testing.T) {
		vs := lintStrict(enumSchema(251, 60))
		if !has(vs, "maxNameChars") {
			t.Fatalf("251-value enum >15000 chars should flag: %+v", vs)
		}
	})
}

func TestE11Truncation(t *testing.T) {
	l := testLinter(t)
	props := map[string]any{}
	for i := 0; i < 500; i++ {
		props[fmt.Sprintf("x-%d", i)] = i
	}
	schema := map[string]any{"type": "object"}
	for k, v := range props {
		schema[k] = v
	}
	body, _ := json.Marshal(map[string]any{
		"model":    "m",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
		"tools": []any{map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":       "x",
				"parameters": schema,
			},
		}},
	})
	res := l.LintRequest("openai.chat", body)
	if len(res.Ignored) < specvalidate.MaxFindings {
		t.Fatalf("want at least %d findings, got %d", specvalidate.MaxFindings, len(res.Ignored))
	}
	last := res.Ignored[len(res.Ignored)-1]
	if last.Kind != "truncated" {
		t.Fatalf("last finding should be truncated: %+v", last)
	}
	// 200 kept + 1 truncated marker
	if len(res.Ignored) != specvalidate.MaxFindings+1 {
		t.Fatalf("want %d findings + truncated, got %d", specvalidate.MaxFindings, len(res.Ignored))
	}
}

func nestedObject(depth int) map[string]any {
	// measureSchema starts the root at depth 1 and adds 1 per property descent.
	// nestedObject(d) produces a schema whose measured maxDepth is d.
	if depth <= 1 {
		return map[string]any{"type": "object", "additionalProperties": false}
	}
	cur := map[string]any{"type": "string"}
	for i := 0; i < depth-2; i++ {
		cur = map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties":           map[string]any{"c": cur},
			"required":             []any{"c"},
		}
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           map[string]any{"c": cur},
		"required":             []any{"c"},
	}
}

func nProperties(n int) map[string]any {
	props := make(map[string]any, n)
	req := make([]any, 0, n)
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("p%d", i)
		props[name] = map[string]any{"type": "string"}
		req = append(req, name)
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           props,
		"required":             req,
	}
}

func enumSchema(count, valueLen int) map[string]any {
	vals := make([]any, count)
	for i := 0; i < count; i++ {
		vals[i] = fmt.Sprintf("%s%04d", strings.Repeat("v", valueLen), i)
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"color": map[string]any{"type": "string", "enum": vals},
		},
		"required": []any{"color"},
	}
}
