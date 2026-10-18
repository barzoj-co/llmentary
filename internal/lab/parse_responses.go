// SPDX-License-Identifier: AGPL-3.0-only

package lab

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/barzoj-co/llm-test-lab/internal/openaiapi"
)

func parseResponses(call *Call) {
	var req openaiapi.CreateResponse
	if err := json.Unmarshal(call.RawRequest, &req); err == nil {
		if req.Model != nil {
			call.Normalized.Model.Requested = unionString(*req.Model)
		}
		if req.PreviousResponseId != nil {
			call.Normalized.PreviousResponseID = *req.PreviousResponseId
		}
		call.Normalized.ContextServerSide = call.Normalized.PreviousResponseID != "" || req.Conversation != nil
		instructions := ""
		if req.Instructions != nil {
			instructions = *req.Instructions
		}
		var inputRaw json.RawMessage
		if req.Input != nil {
			inputRaw, _ = json.Marshal(*req.Input)
		}
		call.Normalized.SystemPrompt = responsesSystemPrompt(instructions, inputRaw)
		call.Normalized.SystemPromptHash = systemHash(call.Normalized.SystemPrompt)
		call.Normalized.MessagesCount = responsesInputCount(inputRaw)
		if req.Tools != nil {
			call.Normalized.ToolsDeclared = responsesTools(*req.Tools)
		}
		call.Normalized.ToolResultsIn = responsesToolResults(inputRaw)
	}
	if call.Stream {
		parseResponsesStream(call)
	} else {
		parseResponsesObject(call, call.RawResponse, "body")
	}
	if call.Normalized.Error == nil {
		call.Normalized.Error = errorFromHTTP(call.HTTPStatus, call.RawResponse)
	}
	if call.Normalized.FinishReason == "" {
		call.Normalized.FinishReason = "unknown"
	}
}

func parseResponsesStream(call *Call) {
	complete := false
	fallbackItems := map[int]openaiapi.OutputItem{}
	argDeltas := map[string]*strings.Builder{}
	for _, event := range call.SSEEvents {
		if event.Comment != "" || len(bytes.TrimSpace(event.Data)) == 0 {
			continue
		}
		var streamEvent openaiapi.ResponseStreamEvent
		// Mid-stream errors arrive as type=error. The official spec uses
		// top-level code/message (ResponseErrorEvent); some live payloads nest
		// {"error":{...}}. Handle both from the raw frame, not only the typed
		// discriminator path.
		if err := midStreamError(call.HTTPStatus, event.Data); err != nil {
			call.Normalized.Error = err
			call.Normalized.FinishReason = "error"
		}
		if err := json.Unmarshal(event.Data, &streamEvent); err != nil {
			call.Normalized.ParserWarnings = append(call.Normalized.ParserWarnings, warnStrictDecodeFailed+": "+err.Error())
			continue
		}
		typed, err := streamEvent.ValueByDiscriminator()
		if err != nil {
			name := event.Event
			if name == "" {
				var probe struct {
					Type string `json:"type"`
				}
				_ = json.Unmarshal(event.Data, &probe)
				name = probe.Type
			}
			if name == "" {
				name = "unknown"
			}
			call.Normalized.ParserWarnings = append(call.Normalized.ParserWarnings, "unknown responses stream event: "+name)
			continue
		}
		switch ev := typed.(type) {
		case openaiapi.ResponseCompletedEvent:
			complete = true
			parseResponsesObject(call, jsonField(event.Data, "response"), "terminal_event")
		case openaiapi.ResponseFailedEvent:
			complete = true
			parseResponsesObject(call, jsonField(event.Data, "response"), "terminal_event")
		case openaiapi.ResponseIncompleteEvent:
			complete = true
			parseResponsesObject(call, jsonField(event.Data, "response"), "terminal_event")
		case openaiapi.ResponseOutputItemDoneEvent:
			fallbackItems[ev.OutputIndex] = ev.Item
		case openaiapi.ResponseFunctionCallArgumentsDeltaEvent:
			if ev.ItemId != "" {
				if argDeltas[ev.ItemId] == nil {
					argDeltas[ev.ItemId] = &strings.Builder{}
				}
				argDeltas[ev.ItemId].WriteString(ev.Delta)
			}
		case openaiapi.ResponseTextDeltaEvent:
			call.Normalized.OutputText += ev.Delta
		case openaiapi.ResponseRefusalDeltaEvent:
			call.Normalized.Refusal += ev.Delta
		case openaiapi.ResponseRefusalDoneEvent:
			if ev.Refusal != "" {
				call.Normalized.Refusal = ev.Refusal
			}
		case openaiapi.ResponseErrorEvent:
			raw, _ := json.Marshal(ev)
			if err := midStreamError(call.HTTPStatus, raw); err != nil {
				call.Normalized.Error = err
			}
			call.Normalized.FinishReason = "error"
		}
	}
	if !complete {
		call.Normalized.StreamTruncated = true
	} else {
		call.Normalized.StreamTruncated = false
	}
	if len(call.Normalized.ToolCalls) == 0 && len(fallbackItems) > 0 {
		for index, item := range fallbackItems {
			if tool, ok := toolCallFromOutputItem(index, item); ok {
				if b := argDeltas[tool.ItemID]; b != nil && tool.ArgumentsRaw == "" {
					tool.ArgumentsRaw = b.String()
					tool.Arguments, tool.ArgsParseError = parseJSONArguments(tool.ArgumentsRaw)
				}
				call.Normalized.ToolCalls = append(call.Normalized.ToolCalls, tool)
			}
		}
		sortToolCalls(call.Normalized.ToolCalls)
		if len(call.Normalized.ToolCalls) > 0 && call.Normalized.FinishReason == "" {
			call.Normalized.FinishReason = "tool_calls"
		}
	}
}

func parseResponsesObject(call *Call, raw []byte, usageSource string) {
	var resp openaiapi.Response
	if err := json.Unmarshal(raw, &resp); err != nil {
		applyResponsesFallback(call, raw, warnStrictDecodeFailed+": "+err.Error(), usageSource)
		return
	}
	if call.HTTPStatus < 400 {
		noteMissingRequired(call, raw, "id")
	}
	if resp.Id != "" {
		call.RequestIDs["body.id"] = resp.Id
	}
	if model := unionString(resp.Model); model != "" {
		call.Normalized.Model.Returned = model
	}
	objectText := ""
	if resp.OutputText != nil {
		objectText = *resp.OutputText
	}
	fromOutputTextField := objectText != ""
	objectRefusal := ""
	if usageRaw := jsonField(raw, "usage"); len(usageRaw) > 0 {
		call.Normalized.Usage = usageFromResponses(usageRaw, usageSource)
	}
	status := ""
	if resp.Status != nil {
		status = string(*resp.Status)
	}
	call.Normalized.NativeFinishReason = status
	reason := ""
	if resp.IncompleteDetails != nil && resp.IncompleteDetails.Reason != nil {
		reason = string(*resp.IncompleteDetails.Reason)
		if reason != "" {
			call.Normalized.NativeFinishReason += ":" + reason
		}
	}
	for i, item := range resp.Output {
		if tool, ok := toolCallFromOutputItem(i, item); ok {
			call.Normalized.ToolCalls = append(call.Normalized.ToolCalls, tool)
			continue
		}
		typed, err := item.ValueByDiscriminator()
		if err != nil {
			continue
		}
		switch out := typed.(type) {
		case openaiapi.OutputMessage:
			for _, part := range out.Content {
				switch content := mustOutputContent(part).(type) {
				case openaiapi.OutputTextContent:
					if !fromOutputTextField {
						objectText += content.Text
					}
				case openaiapi.RefusalContent:
					objectRefusal += content.Refusal
				}
			}
		case openaiapi.ReasoningItem:
			if call.Normalized.Reasoning == nil {
				call.Normalized.Reasoning = &Reasoning{Present: true}
			}
			if out.EncryptedContent != nil && *out.EncryptedContent != "" {
				call.Normalized.Reasoning.Encrypted = true
			}
			if out.Content != nil {
				for _, part := range *out.Content {
					call.Normalized.Reasoning.Chars += len(part.Text)
				}
			}
		}
	}
	if objectText != "" {
		call.Normalized.OutputText = objectText
	}
	if objectRefusal != "" {
		call.Normalized.Refusal = objectRefusal
	}
	sortToolCalls(call.Normalized.ToolCalls)
	if len(call.Normalized.ToolCalls) > 0 {
		call.Normalized.FinishReason = "tool_calls"
	} else {
		call.Normalized.FinishReason = synthesizeResponsesFinish(status, reason)
	}
	if resp.Error != nil {
		code := string(resp.Error.Code)
		typ := ""
		if resp.Error.Type != nil {
			typ = *resp.Error.Type
		}
		call.Normalized.Error = &CallError{
			HTTPStatus: call.HTTPStatus,
			Type:       typ,
			Code:       code,
			Message:    resp.Error.Message,
			Normalized: normalizeError(call.HTTPStatus, code, typ),
			MidStream:  call.HTTPStatus < 400,
		}
		call.Normalized.FinishReason = "error"
	}
	applyResponsesExtensions(call, raw)
}

func mustOutputContent(part openaiapi.OutputMessageContent) any {
	typed, err := part.ValueByDiscriminator()
	if err != nil {
		return nil
	}
	return typed
}

func toolCallFromOutputItem(index int, item openaiapi.OutputItem) (ToolCall, bool) {
	typed, err := item.ValueByDiscriminator()
	if err != nil {
		return ToolCall{}, false
	}
	switch out := typed.(type) {
	case openaiapi.FunctionToolCall:
		args, argErr := parseJSONArguments(out.Arguments)
		itemID := ""
		if out.Id != nil {
			itemID = *out.Id
		}
		return ToolCall{
			Index: index, ID: out.CallId, ItemID: itemID, Name: out.Name, Kind: "function",
			ArgumentsRaw: out.Arguments, Arguments: args, ArgsParseError: argErr,
		}, true
	case openaiapi.CustomToolCall:
		itemID := ""
		if out.Id != nil {
			itemID = *out.Id
		}
		return ToolCall{
			Index: index, ID: out.CallId, ItemID: itemID, Name: out.Name, Kind: "custom",
			ArgumentsRaw: out.Input,
		}, true
	default:
		return ToolCall{}, false
	}
}

func responsesSystemPrompt(instructions string, input json.RawMessage) string {
	var parts []string
	if instructions != "" {
		parts = append(parts, instructions)
	}
	var items []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(input, &items) == nil {
		for _, item := range items {
			if item.Role == "system" || item.Role == "developer" {
				parts = append(parts, contentString(item.Content))
			}
		}
	}
	return strings.Join(parts, "\n")
}

func responsesInputCount(input json.RawMessage) int {
	input = bytes.TrimSpace(input)
	if len(input) == 0 {
		return 0
	}
	if input[0] == '"' {
		return 1
	}
	var items []json.RawMessage
	if json.Unmarshal(input, &items) == nil {
		return len(items)
	}
	return 0
}

func responsesTools(tools openaiapi.ToolsArray) []ToolDecl {
	out := make([]ToolDecl, 0, len(tools))
	for _, tool := range tools {
		raw, err := json.Marshal(tool)
		if err != nil {
			continue
		}
		var t struct {
			Type       string          `json:"type"`
			Name       string          `json:"name"`
			Parameters json.RawMessage `json:"parameters"`
		}
		if json.Unmarshal(raw, &t) != nil {
			continue
		}
		name := t.Name
		kind := t.Type
		if kind == "" {
			kind = "function"
		}
		if kind != "function" && name == "" {
			name = kind
			kind = "builtin:" + kind
		}
		out = append(out, ToolDecl{Name: name, Type: kind, SchemaHash: schemaHash(t.Parameters)})
	}
	return out
}

func responsesToolResults(input json.RawMessage) []ToolResult {
	var items []struct {
		Type   string          `json:"type"`
		CallID string          `json:"call_id"`
		Name   string          `json:"name"`
		Output json.RawMessage `json:"output"`
	}
	if json.Unmarshal(input, &items) != nil {
		return nil
	}
	var out []ToolResult
	for _, item := range items {
		if item.Type != "function_call_output" {
			continue
		}
		out = append(out, ToolResult{CallID: item.CallID, Name: item.Name, OutputHash: hashBytes(item.Output)})
	}
	return out
}

func synthesizeResponsesFinish(status, reason string) string {
	switch status {
	case "completed":
		return "stop"
	case "failed", "cancelled":
		return "error"
	case "incomplete":
		if reason == "content_filter" {
			return "content_filter"
		}
		if reason == "max_output_tokens" {
			return "length"
		}
		return "incomplete"
	default:
		if status != "" {
			return status
		}
		return "unknown"
	}
}
