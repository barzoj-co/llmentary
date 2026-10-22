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
		typ, _ := obj["type"].(string)
		if typ == "reasoning" {
			if chars := reasoningItemChars(obj); chars > 0 {
				info.WithReasoning++
				info.Chars += chars
			}
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

func reasoningItemChars(obj map[string]any) int {
	return textValueChars(obj["summary"]) + textValueChars(obj["content"]) + reasoningFieldChars(obj)
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
		n += reasoningDetailsChars(raw)
	}
	return n
}

func reasoningDetailsChars(raw any) int {
	return textValueChars(raw)
}

func textValueChars(v any) int {
	switch t := v.(type) {
	case string:
		return len(t)
	case []any:
		n := 0
		for _, item := range t {
			n += textValueChars(item)
		}
		return n
	case map[string]any:
		n := 0
		if s, ok := t["text"].(string); ok {
			n += len(s)
		}
		if s, ok := t["content"].(string); ok {
			n += len(s)
		}
		return n
	default:
		return 0
	}
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
