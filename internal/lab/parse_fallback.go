// SPDX-License-Identifier: AGPL-3.0-only

package lab

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
)

const (
	warnStrictDecodeFailed = "strict decode failed; used tolerant extraction"
	warnUnknownFinish      = "unknown finish_reason"
	warnMissingRequired    = "missing required field"
)

type looseChatDelta struct {
	content    string
	refusal    string
	finish     string
	toolDeltas []looseToolDelta
	usage      json.RawMessage
	model      string
	id         string
	err        *CallError
}

type looseToolDelta struct {
	index int
	id    string
	kind  string
	name  string
	args  string
}

func applyChatFallback(call *Call, raw []byte, reason string) {
	call.Normalized.ParserWarnings = append(call.Normalized.ParserWarnings, reason)
	got := extractLooseChat(raw)
	if got.model != "" && call.Normalized.Model.Returned == "" {
		call.Normalized.Model.Returned = got.model
	}
	if got.id != "" {
		call.RequestIDs["body.id"] = got.id
	}
	if got.content != "" {
		call.Normalized.OutputText += got.content
	}
	if got.refusal != "" {
		call.Normalized.Refusal += got.refusal
	}
	if got.finish != "" {
		call.Normalized.FinishReason = got.finish
	}
	if len(got.usage) > 0 {
		if u := usageFromLoose(got.usage, "body"); u != nil {
			call.Normalized.Usage = u
		}
	}
	for _, item := range got.toolDeltas {
		args, argErr := parseJSONArguments(item.args)
		call.Normalized.ToolCalls = append(call.Normalized.ToolCalls, ToolCall{
			Index: item.index, ID: item.id, Name: item.name, Kind: coalesce(item.kind, "function"),
			ArgumentsRaw: item.args, Arguments: args, ArgsParseError: argErr,
		})
		if argErr != "" {
			call.Normalized.ParserWarnings = append(call.Normalized.ParserWarnings, "tool args not valid JSON for "+item.name+": "+argErr)
		}
	}
	if got.err != nil && call.Normalized.Error == nil {
		call.Normalized.Error = got.err
	}
}

func extractLooseChat(raw []byte) looseChatDelta {
	var out looseChatDelta
	var root map[string]json.RawMessage
	if json.Unmarshal(raw, &root) != nil {
		return out
	}
	out.model = rawString(root["model"])
	out.id = rawString(root["id"])
	if u := root["usage"]; len(u) > 0 {
		out.usage = u
	}
	if err := errorFromHTTP(400, raw); err != nil && err.Message != "" {
		out.err = err
	}
	var choices []map[string]json.RawMessage
	if json.Unmarshal(root["choices"], &choices) != nil {
		return out
	}
	for _, choice := range choices {
		if fr := rawString(choice["finish_reason"]); fr != "" {
			out.finish = fr
		}
		msg := choice["message"]
		if len(bytes.TrimSpace(msg)) == 0 {
			msg = choice["delta"]
		}
		var m map[string]json.RawMessage
		if json.Unmarshal(msg, &m) != nil {
			continue
		}
		if c := rawString(m["content"]); c != "" {
			out.content += c
		}
		if r := rawString(m["refusal"]); r != "" {
			out.refusal += r
		}
		var tools []map[string]json.RawMessage
		if json.Unmarshal(m["tool_calls"], &tools) != nil {
			continue
		}
		for i, tool := range tools {
			item := looseToolDelta{index: i, kind: "function"}
			if n, ok := coerceInt(tool["index"]); ok {
				item.index = *n
			}
			item.id = rawString(tool["id"])
			if t := rawString(tool["type"]); t != "" {
				item.kind = t
			}
			var fn map[string]json.RawMessage
			if json.Unmarshal(tool["function"], &fn) == nil {
				item.name = rawString(fn["name"])
				item.args = rawString(fn["arguments"])
			}
			out.toolDeltas = append(out.toolDeltas, item)
		}
	}
	return out
}

func extractLooseChatChunk(raw []byte) looseChatDelta {
	got := extractLooseChat(raw)
	return got
}

func applyResponsesFallback(call *Call, raw []byte, reason, usageSource string) {
	call.Normalized.ParserWarnings = append(call.Normalized.ParserWarnings, reason)
	var root map[string]json.RawMessage
	if json.Unmarshal(raw, &root) != nil {
		return
	}
	if id := rawString(root["id"]); id != "" {
		call.RequestIDs["body.id"] = id
	}
	if model := rawString(root["model"]); model != "" {
		call.Normalized.Model.Returned = model
	}
	if u := root["usage"]; len(u) > 0 {
		if usage := usageFromLoose(u, usageSource); usage != nil {
			call.Normalized.Usage = usage
		}
	}
	if status := rawString(root["status"]); status != "" {
		call.Normalized.NativeFinishReason = status
		if call.Normalized.FinishReason == "" {
			call.Normalized.FinishReason = synthesizeResponsesFinish(status, rawString(jsonField(root["incomplete_details"], "reason")))
		}
	}
	var output []map[string]json.RawMessage
	if json.Unmarshal(root["output"], &output) == nil {
		for i, item := range output {
			typ := rawString(item["type"])
			switch typ {
			case "function_call", "custom_tool_call":
				kind := "function"
				args := rawString(item["arguments"])
				name := rawString(item["name"])
				if typ == "custom_tool_call" {
					kind = "custom"
					args = rawString(item["input"])
				}
				parsed, argErr := parseJSONArguments(args)
				call.Normalized.ToolCalls = append(call.Normalized.ToolCalls, ToolCall{
					Index: i, ID: rawString(item["call_id"]), ItemID: rawString(item["id"]),
					Name: name, Kind: kind, ArgumentsRaw: args, Arguments: parsed, ArgsParseError: argErr,
				})
			case "message":
				var content []map[string]json.RawMessage
				if json.Unmarshal(item["content"], &content) != nil {
					continue
				}
				for _, part := range content {
					switch rawString(part["type"]) {
					case "output_text":
						call.Normalized.OutputText += rawString(part["text"])
					case "refusal":
						call.Normalized.Refusal += rawString(part["refusal"])
					}
				}
			}
		}
	}
	if text := rawString(root["output_text"]); text != "" {
		call.Normalized.OutputText = text
	}
	if errRaw := root["error"]; len(bytes.TrimSpace(errRaw)) > 0 && !bytes.Equal(bytes.TrimSpace(errRaw), []byte("null")) {
		if typ, code, message, ok := parseLooseError(errRaw); ok {
			call.Normalized.Error = &CallError{
				HTTPStatus: call.HTTPStatus, Type: typ, Code: code, Message: message,
				Normalized: normalizeError(call.HTTPStatus, code, typ),
			}
			call.Normalized.FinishReason = "error"
		}
	}
	if len(call.Normalized.ToolCalls) > 0 && call.Normalized.FinishReason == "" {
		call.Normalized.FinishReason = "tool_calls"
	}
}

func usageFromLoose(raw json.RawMessage, source string) *Usage {
	if hasJSONKey(raw, "prompt_tokens") || hasJSONKey(raw, "completion_tokens") {
		if u := usageFromChat(raw, source); u != nil {
			return u
		}
	}
	if hasJSONKey(raw, "input_tokens") || hasJSONKey(raw, "output_tokens") {
		if u := usageFromResponses(raw, source); u != nil {
			return u
		}
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	usage := &Usage{Source: source, Raw: append(json.RawMessage(nil), raw...)}
	set := false
	if n, ok := coerceInt(m["prompt_tokens"]); ok {
		usage.InputTokens = n
		set = true
	}
	if n, ok := coerceInt(m["input_tokens"]); ok {
		usage.InputTokens = n
		set = true
	}
	if n, ok := coerceInt(m["completion_tokens"]); ok {
		usage.OutputTokens = n
		set = true
	}
	if n, ok := coerceInt(m["output_tokens"]); ok {
		usage.OutputTokens = n
		set = true
	}
	if n, ok := coerceInt(m["total_tokens"]); ok {
		usage.TotalTokens = n
		set = true
	}
	if details := m["prompt_tokens_details"]; len(details) > 0 {
		var d map[string]json.RawMessage
		if json.Unmarshal(details, &d) == nil {
			if n, ok := coerceInt(d["cached_tokens"]); ok {
				usage.CachedInputTokens = n
				set = true
			}
			if n, ok := coerceInt(d["audio_tokens"]); ok {
				usage.AudioTokens = n
				set = true
			}
		}
	}
	if details := m["completion_tokens_details"]; len(details) > 0 {
		var d map[string]json.RawMessage
		if json.Unmarshal(details, &d) == nil {
			if n, ok := coerceInt(d["reasoning_tokens"]); ok {
				usage.ReasoningTokens = n
				set = true
			}
			if n, ok := coerceInt(d["accepted_prediction_tokens"]); ok {
				usage.AcceptedPredictionTokens = n
				set = true
			}
			if n, ok := coerceInt(d["rejected_prediction_tokens"]); ok {
				usage.RejectedPredictionTokens = n
				set = true
			}
			if usage.AudioTokens == nil {
				if n, ok := coerceInt(d["audio_tokens"]); ok {
					usage.AudioTokens = n
					set = true
				}
			}
		}
	}
	if !set {
		return nil
	}
	applyUsageExtensions(usage, raw)
	return usage
}

func rawString(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return strings.Trim(string(raw), `"`)
}

func coerceInt(raw json.RawMessage) (*int, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, false
	}
	var n int
	if json.Unmarshal(raw, &n) == nil {
		return intPtr(n), true
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		i, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil {
			return nil, false
		}
		return intPtr(i), true
	}
	return nil, false
}

func warnUnknownFinishReason(call *Call, reason string, valid bool) {
	if reason == "" || valid {
		return
	}
	call.Normalized.ParserWarnings = append(call.Normalized.ParserWarnings, warnUnknownFinish+": "+reason)
}

func noteMissingRequired(call *Call, raw []byte, keys ...string) {
	for _, key := range keys {
		if !hasJSONKey(raw, key) {
			call.Normalized.ParserWarnings = append(call.Normalized.ParserWarnings, warnMissingRequired+": "+key)
		}
	}
}
