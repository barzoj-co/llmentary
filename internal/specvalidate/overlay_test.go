// SPDX-License-Identifier: AGPL-3.0-only

package specvalidate

import (
	"strings"
	"testing"
)

func TestParseOverlayErrors(t *testing.T) {
	t.Parallel()
	if _, err := ParseOverlay([]byte(":\n  -")); err == nil {
		t.Fatal("invalid yaml")
	}
	if _, err := ParseOverlay([]byte("overlay: 1.0\nentries: []\n")); err == nil || !strings.Contains(err.Error(), "no entries") {
		t.Fatalf("empty: %v", err)
	}
	if _, err := ParseOverlay([]byte("entries:\n  - why: x\n")); err == nil || !strings.Contains(err.Error(), "missing id") {
		t.Fatalf("id: %v", err)
	}
	if _, err := ParseOverlay([]byte("entries:\n  - id: a\n    added: 2026-01-01\n    action: reopen_object\n")); err == nil || !strings.Contains(err.Error(), "live_test") {
		t.Fatalf("evidence: %v", err)
	}
	if _, err := ParseOverlay([]byte("entries:\n  - id: a\n    evidence:\n      live_test: T\n    action: reopen_object\n")); err == nil || !strings.Contains(err.Error(), "added") {
		t.Fatalf("added: %v", err)
	}
	if _, err := ParseOverlay([]byte("entries:\n  - id: a\n    evidence:\n      live_test: T\n    added: 2026-01-01\n")); err == nil || !strings.Contains(err.Error(), "action") {
		t.Fatalf("action: %v", err)
	}
}

func TestApplyOverlayActionsAndHelpers(t *testing.T) {
	t.Parallel()
	ov := Overlay{
		Prechecks: OverlayChecks{Skip: []OverlaySkip{{ID: "utf8BOM"}}},
		Entries: []OverlayEntry{
			{ID: "reopen", Action: "reopen_object", Schemas: []string{"Msg", "Missing"}, Evidence: OverlayEvidence{LiveTest: "T"}, Added: "2026-01-01"},
			{ID: "req", Action: "remove_required", Property: "foo", Schemas: []string{"Msg"}, Evidence: OverlayEvidence{LiveTest: "T"}, Added: "2026-01-01"},
		},
	}
	if !ov.SkipsPrecheck("utf8BOM") || ov.SkipsPrecheck("other") {
		t.Fatal("skip")
	}
	ids := ov.EntryIDs()
	if len(ids) != 2 || ids[0] != "reopen" || ids[1] != "req" {
		t.Fatalf("ids=%v", ids)
	}

	spec := map[string]any{
		"components": map[string]any{
			"schemas": map[string]any{
				"Msg": map[string]any{
					"additionalProperties":  false,
					"unevaluatedProperties": false,
					"required":              []any{"foo", "bar"},
				},
			},
		},
	}
	if err := ApplyOverlay(spec, ov); err != nil {
		t.Fatal(err)
	}
	msg := spec["components"].(map[string]any)["schemas"].(map[string]any)["Msg"].(map[string]any)
	if _, ok := msg["additionalProperties"]; ok {
		t.Fatal("additionalProperties still present")
	}
	req, _ := msg["required"].([]any)
	if len(req) != 1 || req[0] != "bar" {
		t.Fatalf("required=%v", req)
	}

	if err := ApplyOverlay(map[string]any{}, ov); err == nil || !strings.Contains(err.Error(), "components.schemas") {
		t.Fatalf("no schemas: %v", err)
	}
	bad := Overlay{Entries: []OverlayEntry{{ID: "x", Action: "remove_required", Schemas: []string{"Msg"}}}}
	if err := ApplyOverlay(spec, bad); err == nil || !strings.Contains(err.Error(), "property") {
		t.Fatalf("missing property: %v", err)
	}
	unk := Overlay{Entries: []OverlayEntry{{ID: "x", Action: "nope", Schemas: []string{"Msg"}}}}
	if err := ApplyOverlay(spec, unk); err == nil || !strings.Contains(err.Error(), "unknown action") {
		t.Fatalf("unknown: %v", err)
	}
	if componentSchemas(map[string]any{"components": map[string]any{}}) != nil {
		t.Fatal("empty schemas")
	}
}

func TestCompileRejectsBadInputs(t *testing.T) {
	t.Parallel()
	if _, err := Compile([]byte(`{}`), []byte("entries: []"), "", ""); err == nil {
		t.Fatal("bad overlay")
	}
	if _, err := Compile([]byte(`{`), EmbeddedOverlay(), "", ""); err == nil {
		t.Fatal("bad spec")
	}
}

func TestCompileRegexp2AndEmbeddedAccessors(t *testing.T) {
	t.Parallel()
	if _, err := compileRegexp2("("); err == nil {
		t.Fatal("invalid regexp")
	}
	re, err := compileRegexp2("^hi$")
	if err != nil {
		t.Fatal(err)
	}
	if !re.MatchString("hi") || re.MatchString("no") || re.String() == "" {
		t.Fatalf("re=%v", re)
	}
	if len(EmbeddedSubspec()) == 0 || len(EmbeddedOverlay()) == 0 {
		t.Fatal("empty embeds")
	}
	sha, retrieved := parseMeta("ignored\nspec_sha=abc\nschema_subset_retrieved=2026-01-01\n")
	if sha != "abc" || retrieved != "2026-01-01" {
		t.Fatalf("%s %s", sha, retrieved)
	}
}

func TestApplyBothProfileCorrectionsAndClone(t *testing.T) {
	t.Parallel()
	applyBothProfileCorrections(map[string]any{})
	spec := map[string]any{
		"components": map[string]any{
			"schemas": map[string]any{
				"CreateResponse": map[string]any{"required": []any{"input"}},
			},
		},
	}
	applyBothProfileCorrections(spec)
	req := spec["components"].(map[string]any)["schemas"].(map[string]any)["CreateResponse"].(map[string]any)["required"].([]any)
	found := false
	for _, item := range req {
		if item == "model" {
			found = true
		}
	}
	if !found {
		t.Fatalf("required=%v", req)
	}
	applyBothProfileCorrections(spec) // already has model
	if cloneMap(nil) != nil {
		t.Fatal("nil clone")
	}
}

func TestValidateRequestSkipAndResponseDone(t *testing.T) {
	eng, err := CompileEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	res := eng.ValidateRequest(ProfileRelaxed, "openai.chat", bytesRepeat(eng, 8), 4)
	if res.Checked || res.SkippedReason != "body_too_large" {
		t.Fatalf("%+v", res)
	}
	res = eng.ValidateResponse(ProfileStrict, "openai.chat", []byte("   "), false, nil)
	if !res.Checked || len(res.Violations) != 0 {
		t.Fatalf("empty resp %+v", res)
	}
	res = eng.ValidateResponse(ProfileStrict, "openai.chat", []byte("[DONE]"), true, intPtr(0))
	if len(res.Violations) != 0 {
		t.Fatalf("done %+v", res)
	}
	res = eng.ValidateResponse(ProfileRelaxed, "openai.responses", []byte(`{"id":"r"}`), false, nil)
	if !res.Checked || len(res.Violations) == 0 {
		t.Fatalf("relaxed responses body %+v", res)
	}
	res = eng.ValidateRequest(ProfileRelaxed, "openai.responses", []byte(`{"model":"m","input":"hi"}`), 0)
	if !res.Checked {
		t.Fatalf("relaxed responses %+v", res)
	}
}

func bytesRepeat(_ *Engine, n int) []byte {
	return []byte(strings.Repeat("x", n))
}
