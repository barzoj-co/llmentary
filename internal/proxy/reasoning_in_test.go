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
}
