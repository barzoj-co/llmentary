package lab

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/barzoj-co/llm-test-lab/pkg/llmtestlab"
)

type responsesRequest struct {
	Model              string            `json:"model"`
	Instructions       string            `json:"instructions"`
	Input              json.RawMessage   `json:"input"`
	Tools              []json.RawMessage `json:"tools"`
	PreviousResponseID string            `json:"previous_response_id"`
	Conversation       any               `json:"conversation"`
}

func parseResponses(call *llmtestlab.Call) {
	var req responsesRequest
	if err := json.Unmarshal(call.RawRequest, &req); err == nil {
		call.Normalized.Model.Requested = req.Model
		call.Normalized.PreviousResponseID = req.PreviousResponseID
		call.Normalized.ContextServerSide = req.PreviousResponseID != "" || req.Conversation != nil
		call.Normalized.SystemPrompt = responsesSystemPrompt(req.Instructions, req.Input)
		call.Normalized.SystemPromptHash = systemHash(call.Normalized.SystemPrompt)
		call.Normalized.MessagesCount = responsesInputCount(req.Input)
		call.Normalized.ToolsDeclared = responsesTools(req.Tools)
		call.Normalized.ToolResultsIn = responsesToolResults(req.Input)
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

func parseResponsesStream(call *llmtestlab.Call) {
	complete := false
	fallbackItems := map[int]json.RawMessage{}
	argDeltas := map[string]*strings.Builder{}
	for _, event := range call.SSEEvents {
		if event.Comment != "" || len(bytes.TrimSpace(event.Data)) == 0 {
			continue
		}
		name := event.Event
		if name == "" {
			var typed struct {
				Type string `json:"type"`
			}
			_ = json.Unmarshal(event.Data, &typed)
			name = typed.Type
		}
		switch name {
		case "response.completed":
			complete = true
			var wrapper struct {
				Response json.RawMessage `json:"response"`
			}
			if json.Unmarshal(event.Data, &wrapper) == nil && len(wrapper.Response) > 0 {
				parseResponsesObject(call, wrapper.Response, "terminal_event")
			}
		case "response.failed", "response.incomplete":
			var wrapper struct {
				Response json.RawMessage `json:"response"`
			}
			if json.Unmarshal(event.Data, &wrapper) == nil && len(wrapper.Response) > 0 {
				parseResponsesObject(call, wrapper.Response, "terminal_event")
			}
		case "response.output_item.done":
			var wrapper struct {
				OutputIndex int             `json:"output_index"`
				Item        json.RawMessage `json:"item"`
			}
			if json.Unmarshal(event.Data, &wrapper) == nil && len(wrapper.Item) > 0 {
				fallbackItems[wrapper.OutputIndex] = wrapper.Item
			}
		case "response.function_call_arguments.delta":
			var wrapper struct {
				ItemID string `json:"item_id"`
				Delta  string `json:"delta"`
			}
			if json.Unmarshal(event.Data, &wrapper) == nil && wrapper.ItemID != "" {
				if argDeltas[wrapper.ItemID] == nil {
					argDeltas[wrapper.ItemID] = &strings.Builder{}
				}
				argDeltas[wrapper.ItemID].WriteString(wrapper.Delta)
			}
		case "error":
			call.Normalized.Error = midStreamError(call.HTTPStatus, event.Data)
			call.Normalized.FinishReason = "error"
		default:
			if strings.HasPrefix(name, "response.") ||
				strings.HasPrefix(name, "response.output_text.") ||
				strings.HasPrefix(name, "response.content_part.") ||
				strings.HasPrefix(name, "response.reasoning") {
				continue
			}
			call.Normalized.ParserWarnings = append(call.Normalized.ParserWarnings, "unknown responses stream event: "+name)
		}
	}
	if !complete {
		call.Normalized.StreamTruncated = true
	}
	if len(call.Normalized.ToolCalls) == 0 && len(fallbackItems) > 0 {
		for index, raw := range fallbackItems {
			if tool, ok := toolCallFromResponsesItem(index, raw); ok {
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

func parseResponsesObject(call *llmtestlab.Call, raw []byte, usageSource string) {
	var resp struct {
		ID                string `json:"id"`
		Model             string `json:"model"`
		Status            string `json:"status"`
		IncompleteDetails struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
		Usage      json.RawMessage   `json:"usage"`
		Output     []json.RawMessage `json:"output"`
		OutputText string            `json:"output_text"`
		Error      *struct {
			Type    string `json:"type"`
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		ContentFilters []json.RawMessage `json:"content_filters"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		call.Normalized.ParserWarnings = append(call.Normalized.ParserWarnings, "responses parse error: "+err.Error())
		return
	}
	if resp.ID != "" {
		call.RequestIDs["body.id"] = resp.ID
	}
	if resp.Model != "" {
		call.Normalized.Model.Returned = resp.Model
	}
	if resp.OutputText != "" {
		call.Normalized.OutputText = resp.OutputText
	}
	call.Normalized.Usage = usageFromResponses(resp.Usage, usageSource)
	call.Normalized.NativeFinishReason = resp.Status
	if resp.IncompleteDetails.Reason != "" {
		call.Normalized.NativeFinishReason += ":" + resp.IncompleteDetails.Reason
	}
	for i, item := range resp.Output {
		if tool, ok := toolCallFromResponsesItem(i, item); ok {
			call.Normalized.ToolCalls = append(call.Normalized.ToolCalls, tool)
			continue
		}
		var generic struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if json.Unmarshal(item, &generic) == nil {
			for _, content := range generic.Content {
				if content.Text != "" {
					call.Normalized.OutputText += content.Text
				}
			}
			if strings.Contains(generic.Type, "reasoning") {
				call.Normalized.Reasoning = &llmtestlab.Reasoning{Present: true}
			}
		}
	}
	sortToolCalls(call.Normalized.ToolCalls)
	if len(call.Normalized.ToolCalls) > 0 {
		call.Normalized.FinishReason = "tool_calls"
	} else {
		call.Normalized.FinishReason = synthesizeResponsesFinish(resp.Status, resp.IncompleteDetails.Reason)
	}
	if resp.Error != nil {
		call.Normalized.Error = &llmtestlab.CallError{
			HTTPStatus: call.HTTPStatus,
			Type:       resp.Error.Type,
			Code:       resp.Error.Code,
			Message:    resp.Error.Message,
			Normalized: normalizeError(call.HTTPStatus, resp.Error.Code, resp.Error.Type),
			MidStream:  call.HTTPStatus < 400,
		}
		call.Normalized.FinishReason = "error"
	}
	for _, rawFilter := range resp.ContentFilters {
		collectFilter(call, rawFilter)
	}
}

func toolCallFromResponsesItem(index int, raw json.RawMessage) (llmtestlab.ToolCall, bool) {
	var item struct {
		ID        string `json:"id"`
		Type      string `json:"type"`
		CallID    string `json:"call_id"`
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
		Input     string `json:"input"`
	}
	if json.Unmarshal(raw, &item) != nil {
		return llmtestlab.ToolCall{}, false
	}
	switch item.Type {
	case "function_call":
		args, argErr := parseJSONArguments(item.Arguments)
		return llmtestlab.ToolCall{
			Index: index, ID: item.CallID, ItemID: item.ID, Name: item.Name, Kind: "function",
			ArgumentsRaw: item.Arguments, Arguments: args, ArgsParseError: argErr,
		}, true
	case "custom_tool_call":
		return llmtestlab.ToolCall{
			Index: index, ID: item.CallID, ItemID: item.ID, Name: item.Name, Kind: "custom",
			ArgumentsRaw: item.Input,
		}, true
	default:
		return llmtestlab.ToolCall{}, false
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

func responsesTools(rawTools []json.RawMessage) []llmtestlab.ToolDecl {
	out := make([]llmtestlab.ToolDecl, 0, len(rawTools))
	for _, raw := range rawTools {
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
		out = append(out, llmtestlab.ToolDecl{Name: name, Type: kind, SchemaHash: schemaHash(t.Parameters)})
	}
	return out
}

func responsesToolResults(input json.RawMessage) []llmtestlab.ToolResult {
	var items []struct {
		Type   string          `json:"type"`
		CallID string          `json:"call_id"`
		Name   string          `json:"name"`
		Output json.RawMessage `json:"output"`
	}
	if json.Unmarshal(input, &items) != nil {
		return nil
	}
	var out []llmtestlab.ToolResult
	for _, item := range items {
		if item.Type != "function_call_output" {
			continue
		}
		out = append(out, llmtestlab.ToolResult{CallID: item.CallID, Name: item.Name, OutputHash: hashBytes(item.Output)})
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
