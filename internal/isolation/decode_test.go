// SPDX-License-Identifier: AGPL-3.0-only

package isolation

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestDecodeRawRequestShapes(t *testing.T) {
	t.Parallel()
	got, err := decodeRawRequest(nil)
	if err != nil || got != nil {
		t.Fatalf("%v %v", got, err)
	}
	got, err = decodeRawRequest(json.RawMessage("null"))
	if err != nil || got != nil {
		t.Fatalf("null %v %v", got, err)
	}
	obj := map[string]any{"model": "m"}
	raw, _ := json.Marshal(obj)
	got, err = decodeRawRequest(raw)
	if err != nil || got["model"] != "m" {
		t.Fatalf("obj %v %v", got, err)
	}
	quoted, _ := json.Marshal(string(raw))
	got, err = decodeRawRequest(quoted)
	if err != nil || got["model"] != "m" {
		t.Fatalf("quoted json %v %v", got, err)
	}
	b64, _ := json.Marshal(base64.StdEncoding.EncodeToString(raw))
	got, err = decodeRawRequest(b64)
	if err != nil || got["model"] != "m" {
		t.Fatalf("b64 %v %v", got, err)
	}
	if _, err := decodeRawRequest(json.RawMessage(`"not-json"`)); err == nil {
		t.Fatal("expected error")
	}
	if _, err := decodeRawRequest(json.RawMessage(`[]`)); err == nil {
		t.Fatal("array")
	}
}

func TestContentTextAndNormalize(t *testing.T) {
	t.Parallel()
	if contentText("hi") != "hi" {
		t.Fatal("string")
	}
	if contentText(3) != "" {
		t.Fatal("other")
	}
	got := contentText([]any{
		map[string]any{"text": "a"},
		map[string]any{"input_text": "b"},
		map[string]any{"output_text": "c"},
		map[string]any{"other": "x"},
		"skip",
	})
	if got != "abc" {
		t.Fatalf("parts=%q", got)
	}
	if normalizeText("  ") != "" {
		t.Fatal("blank")
	}
	if normalizeText("  Hello\tTHERE  ") == "" {
		t.Fatal("fold")
	}
}
