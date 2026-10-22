// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"bytes"
	"encoding/json"
)

func applyRequestReasoningIn(call *Call, raw []byte) {
	if call == nil || len(bytes.TrimSpace(raw)) == 0 {
		return
	}
	var body map[string]any
	if json.Unmarshal(firstJSONObjectBytes(raw), &body) != nil {
		return
	}
	info := &ReasoningIn{}
	if msgs, ok := body["messages"].([]any); ok {
		scanReasoningMessages(info, msgs)
	}
	if input, ok := body["input"].([]any); ok {
		scanReasoningMessages(info, input)
	}
	if info.AssistantTurns == 0 && info.WithReasoning == 0 && info.Chars == 0 {
		return
	}
	call.Normalized.ReasoningIn = info
}

func scanReasoningMessages(info *ReasoningIn, items []any) {
	for _, item := range items {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		role, _ := obj["role"].(string)
		if role != "assistant" {
			continue
		}
		info.AssistantTurns++
		if chars := reasoningFieldChars(obj); chars > 0 {
			info.WithReasoning++
			info.Chars += chars
		}
	}
}

func reasoningFieldChars(obj map[string]any) int {
	n := 0
	if s, ok := obj["reasoning_content"].(string); ok {
		n += len(s)
	}
	if s, ok := obj["reasoning"].(string); ok {
		n += len(s)
	}
	if raw, ok := obj["reasoning_details"]; ok && raw != nil {
		b, err := json.Marshal(raw)
		if err == nil && !bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
			n += len(bytes.TrimSpace(b))
		}
	}
	return n
}

func firstJSONObjectBytes(raw []byte) []byte {
	raw = bytes.TrimSpace(raw)
	if bytes.HasPrefix(raw, []byte{0xEF, 0xBB, 0xBF}) {
		raw = raw[3:]
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	var v json.RawMessage
	if err := dec.Decode(&v); err != nil {
		return raw
	}
	return v
}
