// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"strings"
	"testing"
)

func TestApplyRequestReasoningInChat(t *testing.T) {
	t.Parallel()
	call := &Call{}
	applyRequestReasoningIn(call, []byte(`{
		"model":"m",
		"messages":[
			{"role":"user","content":"hi","reasoning_content":"ignored"},
			{"role":"assistant","content":"hello","reasoning_content":"user greets"},
			{"role":"assistant","content":"again"},
			{"role":"assistant","content":"x","reasoning_details":[{"type":"reasoning.summary","text":"n"}]}
		]
	}`))
	got := call.Normalized.ReasoningIn
	if got == nil {
		t.Fatal("nil")
	}
	if got.AssistantTurns != 3 || got.WithReasoning != 2 || got.Chars == 0 {
		t.Fatalf("%+v", got)
	}
}

func TestApplyRequestReasoningInResponsesAndEmpty(t *testing.T) {
	t.Parallel()
	call := &Call{}
	applyRequestReasoningIn(call, []byte(`{"model":"m","input":[{"type":"message","role":"assistant","content":"x","reasoning":"think"}]}`))
	if call.Normalized.ReasoningIn == nil || call.Normalized.ReasoningIn.WithReasoning != 1 {
		t.Fatalf("%+v", call.Normalized.ReasoningIn)
	}
	empty := &Call{}
	applyRequestReasoningIn(empty, []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`))
	if empty.Normalized.ReasoningIn != nil {
		t.Fatalf("unexpected %+v", empty.Normalized.ReasoningIn)
	}
	applyRequestReasoningIn(empty, nil)
	applyRequestReasoningIn(nil, []byte(`{}`))
}

func TestReasoningFieldCharsNullDetails(t *testing.T) {
	t.Parallel()
	if reasoningFieldChars(map[string]any{"reasoning_details": nil}) != 0 {
		t.Fatal("null details")
	}
	if reasoningFieldChars(map[string]any{"reasoning_content": ""}) != 0 {
		t.Fatal("empty string is zero chars")
	}
	if n := reasoningFieldChars(map[string]any{"reasoning_content": strings.Repeat("a", 12)}); n != 12 {
		t.Fatalf("n=%d", n)
	}
	if n := reasoningFieldChars(map[string]any{"reasoning_details": []any{}}); n != 0 {
		t.Fatalf("empty details counted %d", n)
	}
	if n := reasoningFieldChars(map[string]any{"reasoning_details": []any{map[string]any{"text": "ab"}}}); n != 2 {
		t.Fatalf("details text=%d", n)
	}
}

func TestScanReasoningTypeItemAndEmptyDetails(t *testing.T) {
	t.Parallel()
	call := &Call{}
	applyRequestReasoningIn(call, []byte(`{"model":"m","input":[{"type":"reasoning","summary":[{"type":"summary_text","text":"thought"}],"content":[{"text":"more"}]}]}`))
	got := call.Normalized.ReasoningIn
	if got == nil || got.AssistantTurns != 0 || got.WithReasoning != 1 || got.Chars != len("thought")+len("more") {
		t.Fatalf("reasoning item %+v", got)
	}
	empty := &Call{}
	applyRequestReasoningIn(empty, []byte(`{"model":"m","messages":[{"role":"assistant","content":"x","reasoning_details":[]}]}`))
	got = empty.Normalized.ReasoningIn
	if got == nil || got.AssistantTurns != 1 || got.WithReasoning != 0 || got.Chars != 0 {
		t.Fatalf("empty details %+v", got)
	}
	mixed := &Call{}
	applyRequestReasoningIn(mixed, []byte(`{"model":"m","input":[{"type":"reasoning","summary":"abc"},{"role":"assistant","content":"x"}]}`))
	got = mixed.Normalized.ReasoningIn
	if got == nil || got.AssistantTurns != 1 || got.WithReasoning != 1 || got.Chars != 3 {
		t.Fatalf("mixed %+v", got)
	}
}

func TestReasoningEncryptedOnlyAndUnicodeChars(t *testing.T) {
	t.Parallel()
	enc := &Call{}
	applyRequestReasoningIn(enc, []byte(`{"model":"m","input":[{"type":"reasoning","summary":[],"encrypted_content":"enc"}]}`))
	if enc.Normalized.ReasoningIn == nil || enc.Normalized.ReasoningIn.WithReasoning != 1 || enc.Normalized.ReasoningIn.Chars != 0 {
		t.Fatalf("encrypted-only %+v", enc.Normalized.ReasoningIn)
	}
	emptyEnc := &Call{}
	applyRequestReasoningIn(emptyEnc, []byte(`{"model":"m","input":[{"type":"reasoning","summary":[],"encrypted_content":""}]}`))
	if emptyEnc.Normalized.ReasoningIn != nil {
		t.Fatalf("empty encrypted %+v", emptyEnc.Normalized.ReasoningIn)
	}
	uni := &Call{}
	applyRequestReasoningIn(uni, []byte(`{"model":"m","messages":[{"role":"assistant","content":"x","reasoning_content":"思考"}]}`))
	if uni.Normalized.ReasoningIn == nil || uni.Normalized.ReasoningIn.Chars != 2 {
		t.Fatalf("unicode chars %+v", uni.Normalized.ReasoningIn)
	}
	details := &Call{}
	applyRequestReasoningIn(details, []byte(`{"model":"m","input":[{"type":"reasoning","summary":[{"text":"思考"}]}]}`))
	if details.Normalized.ReasoningIn == nil || details.Normalized.ReasoningIn.Chars != 2 {
		t.Fatalf("unicode summary %+v", details.Normalized.ReasoningIn)
	}
}
