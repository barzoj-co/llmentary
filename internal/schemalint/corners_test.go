// SPDX-License-Identifier: AGPL-3.0-only

package schemalint

import (
	"strings"
	"testing"

	"github.com/barzoj-co/llmentary/internal/specvalidate"
)

func TestLoadSubsetAndClassify(t *testing.T) {
	t.Parallel()
	if _, err := LoadSubset([]byte(":\n  -")); err == nil {
		t.Fatal("bad yaml")
	}
	if _, err := LoadSubset([]byte("supported: {}\n")); err == nil || !strings.Contains(err.Error(), "retrieved") {
		t.Fatalf("missing retrieved: %v", err)
	}
	s, err := LoadSubset(specvalidate.EmbeddedSchemaSubset)
	if err != nil {
		t.Fatal(err)
	}
	if kind, _, _ := s.Classify("type", false); kind != "" {
		t.Fatalf("supported type=%s", kind)
	}
	kind, sev, act := s.Classify("definitely-not-a-keyword", false)
	if kind == "" || sev == "" || act == "" {
		t.Fatalf("unknown keyword classification %q %q %q", kind, sev, act)
	}
	_, _, _ = s.Classify("definitely-not-a-keyword", true)
	if s.Hint("unknown_request_field") == "" && s.Hint("x-vendor") == "" {
		t.Fatal("hints")
	}
	if !strings.Contains(s.Hint("x-foo"), "vendor") {
		t.Fatalf("x hint=%q", s.Hint("x-foo"))
	}
	if s.Hint("totally-unknown") == "" {
		t.Fatal("default hint")
	}
	empty := Subset{}
	if empty.Hint("x") != "" {
		t.Fatal("nil hints")
	}
}

func TestMeasureSchemaAndStrictCompatible(t *testing.T) {
	t.Parallel()
	l := testLinter(t)
	schema := map[string]any{
		"$defs": map[string]any{
			"node": map[string]any{
				"$ref": "#/$defs/node",
			},
		},
		"type": "object",
		"properties": map[string]any{
			"items": map[string]any{
				"type": "array",
				"items": map[string]any{
					"anyOf": []any{
						map[string]any{"type": "string", "enum": []any{"a", "bb"}},
						map[string]any{"$ref": "#/$defs/node"},
					},
				},
			},
		},
	}
	props, depth, enums, names, _, _ := measureSchema(schema, nil, map[string]struct{}{}, 0)
	if props == 0 || depth == 0 || enums == 0 || names == 0 {
		t.Fatalf("measure props=%d depth=%d enums=%d names=%d", props, depth, enums, names)
	}
	if strictCompatible(nil) || strictCompatible(map[string]any{"anyOf": []any{}}) {
		t.Fatal("incompatible")
	}
	if strictCompatible(map[string]any{"type": "string"}) {
		t.Fatal("string")
	}
	if strictCompatible(map[string]any{"type": "object", "additionalProperties": true}) {
		t.Fatal("open")
	}
	if !strictCompatible(map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           map[string]any{"a": map[string]any{"type": "string"}},
		"required":             []any{"a"},
	}) {
		t.Fatal("strict object")
	}
	if unknownKeys(nil, nil, "", "", l) != nil {
		t.Fatal("nil obj")
	}
	capped := specvalidateCap(make([]specvalidate.Violation, specvalidate.MaxFindings+3))
	if len(capped) != specvalidate.MaxFindings {
		t.Fatalf("cap=%d", len(capped))
	}
	findings := capFindings(make([]Finding, specvalidate.MaxFindings+1))
	if findings[len(findings)-1].Kind != "truncated" {
		t.Fatalf("truncated=%+v", findings[len(findings)-1])
	}
}

func TestLintRequestInvalidAndConfirmFallback(t *testing.T) {
	l := testLinter(t)
	if got := l.LintRequest("openai.chat", []byte(`not-json`)); len(got.Ignored) != 0 {
		t.Fatalf("%+v", got)
	}
	if got := l.LintRequest("other", []byte(`{"model":"m"}`)); len(got.Ignored) != 0 {
		t.Fatalf("%+v", got)
	}
	if l.ConfirmStrictFallback([]byte(`{`), []int{0}) != nil {
		t.Fatal("bad response")
	}
	if got := l.ConfirmStrictFallback([]byte(`{"tools":[]}`), []int{-1, 3}); len(got) != 0 {
		t.Fatalf("%v", got)
	}
	if got := l.ConfirmStrictFallback([]byte(`{"tools":[null,{"strict":true}]}`), []int{0, 1}); len(got) != 0 {
		t.Fatalf("true/null %v", got)
	}
	got := l.ConfirmStrictFallback([]byte(`{"tools":[{"strict":false}]}`), []int{0})
	if len(got) != 1 || got[0].Kind != KindStrictFallback {
		t.Fatalf("%+v", got)
	}

	chat := l.LintRequest("openai.chat", []byte(`{
		"model":"ft:gpt",
		"messages":[null,{"role":"user","content":[{"type":"text","text":"hi","extra":1}],"mystery":true}],
		"tools":[{"type":"function","function":{"name":"f","parameters":{"type":"object"},"mystery":1}}]
	}`))
	_ = chat
	resp := l.LintRequest("openai.responses", []byte(`{"model":"m","tools":[{"type":"function","name":"f","parameters":{"type":"object"}}]}`))
	_ = resp
}

func TestJoinPtrAndFormatAllowed(t *testing.T) {
	t.Parallel()
	s, err := LoadSubset(specvalidate.EmbeddedSchemaSubset)
	if err != nil {
		t.Fatal(err)
	}
	if joinPtr("", "a") != "/a" {
		t.Fatalf("%s", joinPtr("", "a"))
	}
	if !s.FormatAllowed("date-time") && !s.FormatAllowed("email") {
		// subset may document either; just exercise the lookup
		_ = s.FormatAllowed("nope")
	}
}
