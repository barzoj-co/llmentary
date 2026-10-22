// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"bytes"
	"encoding/json"
)

// Provider extensions that are absent from the official OpenAI spec
// (OpenRouter, Azure, and other compatible providers) are read from raw JSON
// so generated types do not drop them from the trace.
func applyChatExtensions(call *Call, raw json.RawMessage, choiceRaw json.RawMessage) {
	if len(raw) == 0 {
		return
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(raw, &root) != nil {
		return
	}
	if call.Normalized.ServedBy == "" {
		if s := jsonStringRaw(root["provider"]); s != "" {
			call.Normalized.ServedBy = s
		}
	}
	if usageRaw := root["usage"]; len(usageRaw) > 0 && call.Normalized.Usage != nil {
		applyUsageExtensions(call.Normalized.Usage, usageRaw)
	}
	applyGroqUsage(call, root)
	if pf := root["prompt_filter_results"]; len(pf) > 0 {
		collectPromptFilter(call, pf)
	}
	var choices []json.RawMessage
	_ = json.Unmarshal(root["choices"], &choices)
	if len(choiceRaw) > 0 {
		choices = []json.RawMessage{choiceRaw}
	}
	for _, choice := range choices {
		var c struct {
			FinishReason         string          `json:"finish_reason"`
			NativeFinishReason   string          `json:"native_finish_reason"`
			Error                json.RawMessage `json:"error"`
			ContentFilterResults json.RawMessage `json:"content_filter_results"`
			Message              json.RawMessage `json:"message"`
			Delta                json.RawMessage `json:"delta"`
		}
		if json.Unmarshal(choice, &c) != nil {
			continue
		}
		if c.NativeFinishReason != "" {
			call.Normalized.NativeFinishReason = c.NativeFinishReason
		}
		if c.FinishReason != "" && call.Normalized.FinishReason == "" {
			applyChatFinishReason(call, c.FinishReason)
		}
		if len(c.Error) > 0 && !bytes.Equal(bytes.TrimSpace(c.Error), []byte("null")) {
			recordChatJSONError(call, raw, c.Error)
		}
		collectFilter(call, c.ContentFilterResults)
		applyReasoningFrom(call, c.Message)
		applyReasoningFrom(call, c.Delta)
	}
	if errRaw := root["error"]; len(errRaw) > 0 && !bytes.Equal(bytes.TrimSpace(errRaw), []byte("null")) {
		recordChatJSONError(call, raw, errRaw)
	}
}

// recordChatJSONError records a JSON error object from a chat body or chunk.
// Non-2xx HTTP responses use the HTTP-error path (mid_stream=false). A 2xx
// stream chunk uses the mid-stream path.
func recordChatJSONError(call *Call, raw json.RawMessage, errRaw json.RawMessage) {
	if call.Normalized.Error != nil {
		return
	}
	if call.HTTPStatus >= 400 {
		wrapped, _ := json.Marshal(map[string]json.RawMessage{"error": errRaw})
		call.Normalized.Error = errorFromHTTP(call.HTTPStatus, wrapped)
		return
	}
	wrapped, _ := json.Marshal(map[string]json.RawMessage{"error": errRaw})
	call.Normalized.Error = midStreamError(call.HTTPStatus, wrapped)
}

func applyResponsesExtensions(call *Call, raw json.RawMessage) {
	var root map[string]json.RawMessage
	if json.Unmarshal(raw, &root) != nil {
		return
	}
	if filters := root["content_filters"]; len(filters) > 0 {
		var items []json.RawMessage
		if json.Unmarshal(filters, &items) == nil {
			for _, item := range items {
				collectFilter(call, item)
			}
		}
	}
}

func applyGroqUsage(call *Call, root map[string]json.RawMessage) {
	if call.Normalized.Usage != nil {
		return
	}
	xg := root["x_groq"]
	if len(bytes.TrimSpace(xg)) == 0 || bytes.Equal(bytes.TrimSpace(xg), []byte("null")) {
		return
	}
	u := jsonField(xg, "usage")
	if len(bytes.TrimSpace(u)) == 0 || bytes.Equal(bytes.TrimSpace(u), []byte("null")) {
		return
	}
	if usage := usageFromChat(u, "x_groq"); usage != nil {
		call.Normalized.Usage = usage
		return
	}
	call.Normalized.Usage = usageFromLoose(u, "x_groq")
}

func applyUsageExtensions(usage *Usage, raw json.RawMessage) {
	if usage == nil || len(raw) == 0 {
		return
	}
	if usage.CostUSD == nil && hasJSONKey(raw, "cost") {
		var holder struct {
			Cost *float64 `json:"cost"`
		}
		if json.Unmarshal(raw, &holder) == nil {
			usage.CostUSD = holder.Cost
		}
	}
}

func applyReasoningFrom(call *Call, raw json.RawMessage) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return
	}
	var m struct {
		Reasoning        string `json:"reasoning"`
		ReasoningContent string `json:"reasoning_content"`
	}
	if json.Unmarshal(raw, &m) != nil {
		return
	}
	text := m.Reasoning + m.ReasoningContent
	if text == "" {
		return
	}
	if call.Normalized.Reasoning == nil {
		call.Normalized.Reasoning = &Reasoning{Present: true}
	}
	call.Normalized.Reasoning.Chars += len(text)
}

func collectFilter(call *Call, raw json.RawMessage) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return
	}
	if call.Normalized.ContentFilter == nil {
		call.Normalized.ContentFilter = &ContentFilter{Categories: map[string]FilterResult{}}
	}
	call.Normalized.ContentFilter.Raw = append(call.Normalized.ContentFilter.Raw, append(json.RawMessage(nil), raw...))
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return
	}
	for key, value := range m {
		if key == "error" {
			call.Normalized.ContentFilter.FilterSystemError = true
			continue
		}
		var result FilterResult
		if json.Unmarshal(value, &result) == nil {
			call.Normalized.ContentFilter.Categories[key] = result
			if result.Filtered {
				call.Normalized.ContentFilter.CompletionFiltered = true
			}
		}
	}
}

func collectPromptFilter(call *Call, raw json.RawMessage) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return
	}
	if call.Normalized.ContentFilter == nil {
		call.Normalized.ContentFilter = &ContentFilter{Categories: map[string]FilterResult{}}
	}
	call.Normalized.ContentFilter.Raw = append(call.Normalized.ContentFilter.Raw, append(json.RawMessage(nil), raw...))
	var items []struct {
		ContentFilterResults json.RawMessage `json:"content_filter_results"`
	}
	if json.Unmarshal(raw, &items) == nil {
		for _, item := range items {
			if len(item.ContentFilterResults) == 0 {
				continue
			}
			var m map[string]json.RawMessage
			if json.Unmarshal(item.ContentFilterResults, &m) != nil {
				continue
			}
			for _, value := range m {
				var result FilterResult
				if json.Unmarshal(value, &result) == nil && result.Filtered {
					call.Normalized.ContentFilter.PromptFiltered = true
				}
			}
		}
	}
}

func jsonStringRaw(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return ""
}
