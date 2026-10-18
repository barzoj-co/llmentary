// SPDX-License-Identifier: AGPL-3.0-only

package schemalint

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/barzoj-co/llm-test-lab/internal/specvalidate"
)

func testLinter(t *testing.T) *Linter {
	t.Helper()
	s, err := LoadSubset(specvalidate.EmbeddedSchemaSubset)
	if err != nil {
		t.Fatal(err)
	}
	return New(s)
}

func TestE1NestedUnknowns(t *testing.T) {
	l := testLinter(t)
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"a": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"b": map[string]any{
							"anyOf": []any{
								map[string]any{"type": "string"},
								map[string]any{"type": "object", "x-foo": 1},
							},
						},
					},
				},
			},
		},
	}
	findings := l.lintSchema(schema, "/tools/0/function/parameters", "", ContextChatToolParams, false, false, nil, map[string]struct{}{})
	found := false
	for _, f := range findings {
		if strings.Contains(f.Path, "properties.a.items.properties.b.anyOf[1]") && f.Key == "x-foo" {
			found = true
		}
	}
	if !found {
		t.Fatalf("findings=%+v", findings)
	}
}

func TestE2RefOnce(t *testing.T) {
	l := testLinter(t)
	schema := map[string]any{
		"type": "object",
		"$defs": map[string]any{
			"step": map[string]any{"type": "string", "enumDescriptions": map[string]any{"a": "b"}},
		},
		"properties": map[string]any{
			"one":   map[string]any{"$ref": "#/$defs/step"},
			"two":   map[string]any{"$ref": "#/$defs/step"},
			"three": map[string]any{"$ref": "#/$defs/step"},
		},
	}
	findings := l.lintSchema(schema, "/tools/0/function/parameters", "", ContextChatToolParams, false, false, nil, map[string]struct{}{})
	n := 0
	for _, f := range findings {
		if f.Key == "enumDescriptions" {
			n++
			if !strings.Contains(f.Pointer, "$defs/step") {
				t.Fatalf("expected definition site, got %s", f.Pointer)
			}
		}
	}
	if n != 1 {
		t.Fatalf("want 1 finding, got %d: %+v", n, findings)
	}
}

func TestE2DanglingRef(t *testing.T) {
	l := testLinter(t)
	schema := map[string]any{"$ref": "#/$defs/missing"}
	findings := l.lintSchema(schema, "/p", "", ContextChatToolParams, false, false, nil, map[string]struct{}{})
	if len(findings) == 0 || findings[0].Kind != KindUnresolvedRef {
		t.Fatalf("findings=%+v", findings)
	}
}

func TestE3Recursion(t *testing.T) {
	l := testLinter(t)
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"child": map[string]any{"$ref": "#"},
		},
		"x-loop": true,
	}
	findings := l.lintSchema(schema, "/p", "", ContextChatToolParams, false, false, nil, map[string]struct{}{})
	n := 0
	for _, f := range findings {
		if f.Key == "x-loop" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("recursion findings=%+v", findings)
	}
}

func TestE7FormatsAndFineTune(t *testing.T) {
	l := testLinter(t)
	email := map[string]any{"type": "string", "format": "email"}
	if fs := l.lintSchema(email, "/p", "", ContextChatToolParams, false, false, nil, map[string]struct{}{}); len(fs) != 0 {
		t.Fatalf("email=%+v", fs)
	}
	uri := map[string]any{"type": "string", "format": "uri"}
	fs := l.lintSchema(uri, "/p", "", ContextChatToolParams, false, false, nil, map[string]struct{}{})
	if len(fs) != 1 || fs[0].Kind != KindUnsupportedFormat {
		t.Fatalf("uri=%+v", fs)
	}
	minL := map[string]any{"type": "string", "minLength": 2}
	fs = l.lintSchema(minL, "/p", "", ContextChatToolParams, false, false, nil, map[string]struct{}{})
	if len(fs) != 1 || fs[0].Kind != KindUnknownKeyword {
		t.Fatalf("minLength=%+v", fs)
	}
	fs = l.lintSchema(map[string]any{"type": "string", "pattern": "x"}, "/p", "", ContextChatToolParams, false, true, nil, map[string]struct{}{})
	if len(fs) != 1 || fs[0].Kind != KindUnsupportedKeyword {
		t.Fatalf("ft pattern=%+v", fs)
	}
}

func TestE8DocumentedUnsupported(t *testing.T) {
	l := testLinter(t)
	for _, key := range []string{"allOf", "not", "if", "dependentRequired", "dependentSchemas"} {
		fs := l.lintSchema(map[string]any{key: []any{}}, "/p", "", ContextChatToolParams, false, false, nil, map[string]struct{}{})
		if len(fs) == 0 || fs[0].Kind != KindUnsupportedKeyword {
			t.Fatalf("%s=%+v", key, fs)
		}
		if fs[0].Hint == "" {
			t.Fatalf("%s missing hint", key)
		}
	}
}

func TestE9LegacyFunctions(t *testing.T) {
	l := testLinter(t)
	body, _ := json.Marshal(map[string]any{
		"model":    "m",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
		"functions": []any{map[string]any{
			"name":       "x",
			"parameters": map[string]any{"type": "object", "enumDescriptions": map[string]any{"a": "b"}},
		}},
	})
	res := l.LintRequest("openai.chat", body)
	if len(res.Ignored) == 0 || res.Ignored[0].Context != ContextChatFunctionsParams {
		t.Fatalf("%+v", res.Ignored)
	}
}

func TestD8SuspiciousEnum(t *testing.T) {
	l := testLinter(t)
	fs := l.lintSchema(map[string]any{
		"enum": []any{map[string]any{"value": 1, "description": "Alabama"}},
	}, "/p", "", ContextChatToolParams, false, false, nil, map[string]struct{}{})
	if len(fs) == 0 || fs[0].Kind != KindSuspiciousEnum {
		t.Fatalf("%+v", fs)
	}
}

func TestEnumKeySortDeterministic(t *testing.T) {
	got := suggestDescription(map[string]any{"description": "Shirt color"}, map[string]any{"red": "warm", "blue": "cool", "amber": "gold"})
	want := "Shirt color. amber: gold; blue: cool; red: warm"
	if got != want {
		t.Fatalf("suggested=%q want %q", got, want)
	}
}

func TestC4OverlayEvidence(t *testing.T) {
	ov, err := specvalidate.ParseOverlay(specvalidate.EmbeddedOverlay())
	if err != nil {
		t.Fatal(err)
	}
	src := readE2ESources(t)
	for _, e := range ov.Entries {
		if !strings.Contains(src, "func "+e.Evidence.LiveTest) {
			t.Errorf("overlay %s evidence %s not found in tests/e2e", e.ID, e.Evidence.LiveTest)
		}
	}
	behRaw, err := os.ReadFile(filepath.Join("..", "..", "tests", "e2e", "testdata", "live-behavior.json"))
	if err != nil {
		t.Fatal(err)
	}
	var beh struct {
		Rows map[string]struct {
			Status            int    `json:"status"`
			ContradictsStrict bool   `json:"contradicts_strict"`
			Overlay           string `json:"overlay"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(behRaw, &beh); err != nil {
		t.Fatal(err)
	}
	ids := map[string]struct{}{}
	for _, e := range ov.Entries {
		ids[e.ID] = struct{}{}
	}
	for _, s := range ov.Prechecks.Skip {
		ids[s.ID] = struct{}{}
	}
	for name, row := range beh.Rows {
		if row.Status == 200 && row.ContradictsStrict {
			if row.Overlay == "" {
				t.Errorf("live-behavior row %s is 200 contradicting strict but has no overlay", name)
				continue
			}
			if _, ok := ids[row.Overlay]; !ok {
				t.Errorf("live-behavior row %s overlay %s is missing from relax-overlay.yaml", name, row.Overlay)
			}
		}
	}
	for _, s := range ov.Prechecks.Skip {
		if s.Evidence.LiveTest != "" && !strings.Contains(src, "func "+s.Evidence.LiveTest) {
			t.Errorf("overlay precheck %s evidence %s not found in tests/e2e", s.ID, s.Evidence.LiveTest)
		}
	}
}

func readE2ESources(t *testing.T) string {
	t.Helper()
	root := filepath.Join("..", "..", "tests", "e2e")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(root, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		b.Write(raw)
		b.WriteByte('\n')
	}
	return b.String()
}
