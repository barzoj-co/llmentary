// SPDX-License-Identifier: AGPL-3.0-only

package lab

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/barzoj-co/llm-test-lab/internal/openaiapi"
)

func parseChat(call *Call) {
	reqBody, reqWarn := normalizeJSONBody(call.RawRequest)
	if reqWarn != "" {
		call.Normalized.ParserWarnings = append(call.Normalized.ParserWarnings, reqWarn)
	}
	var req openaiapi.CreateChatCompletionRequest
	if err := json.Unmarshal(reqBody, &req); err == nil {
		call.Normalized.Model.Requested = unionString(req.Model)
		call.Normalized.MessagesCount = len(req.Messages)
		call.Normalized.SystemPrompt = chatSystemPrompt(req.Messages)
		call.Normalized.SystemPromptHash = systemHash(call.Normalized.SystemPrompt)
		if req.Tools != nil {
			call.Normalized.ToolsDeclared = chatTools(*req.Tools)
		}
		call.Normalized.ToolResultsIn = chatToolResults(req.Messages)
	}
	if call.Stream {
		parseChatStream(call)
	} else {
		parseChatBody(call)
	}
	if call.Normalized.Error == nil {
		call.Normalized.Error = errorFromHTTP(call.HTTPStatus, call.RawResponse)
	}
	if call.Normalized.FinishReason == "" {
		call.Normalized.FinishReason = "unknown"
	}
}

func parseChatBody(call *Call) {
	body, warn := normalizeJSONBody(call.RawResponse)
	if warn != "" {
		call.Normalized.ParserWarnings = append(call.Normalized.ParserWarnings, warn)
	}
	var resp openaiapi.CreateChatCompletionResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		applyChatFallback(call, body, warnStrictDecodeFailed+": "+err.Error())
		applyChatExtensions(call, body, nil)
		return
	}
	if call.HTTPStatus < 400 {
		noteMissingRequired(call, body, "id", "choices")
	}
	if resp.Id != "" {
		call.RequestIDs["body.id"] = resp.Id
	}
	call.Normalized.Model.Returned = resp.Model
	if raw := jsonField(body, "usage"); len(raw) > 0 {
		call.Normalized.Usage = usageFromChat(raw, "body")
	}
	var calls []ToolCall
	for i, choice := range resp.Choices {
		if choice.FinishReason != "" {
			call.Normalized.FinishReason = string(choice.FinishReason)
			warnUnknownFinishReason(call, string(choice.FinishReason), choice.FinishReason.Valid())
		}
		if choice.Message.Content != nil {
			call.Normalized.OutputText += *choice.Message.Content
		}
		if choice.Message.Refusal != nil && *choice.Message.Refusal != "" {
			call.Normalized.Refusal = *choice.Message.Refusal
		}
		if choice.Message.ToolCalls != nil {
			for _, item := range *choice.Message.ToolCalls {
				if tc, ok := toolCallFromChatItem(len(calls), item); ok {
					calls = append(calls, tc)
				}
			}
		}
		_ = i
	}
	call.Normalized.ToolCalls = calls
	applyChatExtensions(call, body, nil)
}

func parseChatStream(call *Call) {
	type acc struct {
		index int
		id    string
		kind  string
		name  string
		args  strings.Builder
	}
	accs := map[int]*acc{}
	// [DONE] is described only in the OpenAPI operation description, not as a
	// schema: https://github.com/openai/openai-openapi — CreateChatCompletion
	// streaming "data: [DONE]" terminator.
	complete := false
	for _, event := range call.SSEEvents {
		if event.Comment != "" || len(bytes.TrimSpace(event.Data)) == 0 {
			continue
		}
		data, dataWarn := normalizeJSONBody(event.Data)
		if dataWarn != "" {
			call.Normalized.ParserWarnings = append(call.Normalized.ParserWarnings, dataWarn)
		}
		event.Data = data
		if bytes.Equal(bytes.TrimSpace(event.Data), []byte("[DONE]")) {
			complete = true
			continue
		}
		// Mid-stream error frames are also description-only, not a schema.
		if err := midStreamError(call.HTTPStatus, event.Data); err != nil {
			call.Normalized.Error = err
		}
		var chunk openaiapi.CreateChatCompletionStreamResponse
		if err := json.Unmarshal(event.Data, &chunk); err != nil {
			call.Normalized.ParserWarnings = append(call.Normalized.ParserWarnings, warnStrictDecodeFailed+": "+err.Error())
			loose := extractLooseChatChunk(event.Data)
			if loose.model != "" {
				call.Normalized.Model.Returned = loose.model
			}
			if loose.id != "" {
				call.RequestIDs["body.id"] = loose.id
			}
			call.Normalized.OutputText += loose.content
			call.Normalized.Refusal += loose.refusal
			if loose.finish != "" {
				call.Normalized.FinishReason = loose.finish
			}
			if len(loose.usage) > 0 {
				if u := usageFromLoose(loose.usage, "final_chunk"); u != nil {
					call.Normalized.Usage = u
				}
			}
			for _, delta := range loose.toolDeltas {
				item := accs[delta.index]
				if item == nil {
					item = &acc{index: delta.index, kind: coalesce(delta.kind, "function")}
					accs[delta.index] = item
				}
				if delta.id != "" {
					item.id = delta.id
				}
				if delta.kind != "" {
					item.kind = delta.kind
				}
				if delta.name != "" {
					item.name = delta.name
				}
				if delta.args != "" {
					item.args.WriteString(delta.args)
				}
			}
			applyChatExtensions(call, event.Data, nil)
			continue
		}
		if chunk.Id != "" {
			call.RequestIDs["body.id"] = chunk.Id
		}
		if chunk.Model != "" {
			call.Normalized.Model.Returned = chunk.Model
		}
		if raw := jsonField(event.Data, "usage"); len(raw) > 0 {
			call.Normalized.Usage = usageFromChat(raw, "final_chunk")
		}
		for _, choice := range chunk.Choices {
			if choice.FinishReason != nil && *choice.FinishReason != "" {
				call.Normalized.FinishReason = string(*choice.FinishReason)
				warnUnknownFinishReason(call, string(*choice.FinishReason), choice.FinishReason.Valid())
			}
			if choice.Delta.Content != nil {
				call.Normalized.OutputText += *choice.Delta.Content
			}
			if choice.Delta.Refusal != nil && *choice.Delta.Refusal != "" {
				call.Normalized.Refusal += *choice.Delta.Refusal
			}
			if choice.Delta.ToolCalls != nil {
				for _, delta := range *choice.Delta.ToolCalls {
					item := accs[delta.Index]
					if item == nil {
						item = &acc{index: delta.Index, kind: "function"}
						accs[delta.Index] = item
					}
					if delta.Id != nil && *delta.Id != "" {
						item.id = *delta.Id
					}
					if delta.Type != nil && *delta.Type != "" {
						item.kind = string(*delta.Type)
					}
					if delta.Function != nil {
						if delta.Function.Name != nil && *delta.Function.Name != "" {
							item.name = *delta.Function.Name
						}
						if delta.Function.Arguments != nil {
							item.args.WriteString(*delta.Function.Arguments)
						}
					}
				}
			}
		}
		applyChatExtensions(call, event.Data, nil)
	}
	if !complete {
		call.Normalized.StreamTruncated = true
	}
	indexes := make([]int, 0, len(accs))
	for index := range accs {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	for _, index := range indexes {
		item := accs[index]
		argsRaw := item.args.String()
		args, argErr := parseJSONArguments(argsRaw)
		call.Normalized.ToolCalls = append(call.Normalized.ToolCalls, ToolCall{
			Index: item.index, ID: item.id, Name: item.name, Kind: item.kind,
			ArgumentsRaw: argsRaw, Arguments: args, ArgsParseError: argErr,
		})
		if argErr != "" {
			call.Normalized.ParserWarnings = append(call.Normalized.ParserWarnings, fmt.Sprintf("tool args not valid JSON for %q: %s", item.name, argErr))
		}
	}
	sortToolCalls(call.Normalized.ToolCalls)
}

func toolCallFromChatItem(index int, item openaiapi.ChatCompletionMessageToolCalls_Item) (ToolCall, bool) {
	if tc, err := item.AsChatCompletionMessageToolCall(); err == nil && tc.Function.Name != "" {
		args, argErr := parseJSONArguments(tc.Function.Arguments)
		return ToolCall{
			Index: index, ID: tc.Id, Name: tc.Function.Name, Kind: coalesce(string(tc.Type), "function"),
			ArgumentsRaw: tc.Function.Arguments, Arguments: args, ArgsParseError: argErr,
		}, true
	}
	if custom, err := item.AsChatCompletionMessageCustomToolCall(); err == nil && custom.Custom.Name != "" {
		return ToolCall{
			Index: index, ID: custom.Id, Name: custom.Custom.Name, Kind: coalesce(string(custom.Type), "custom"),
			ArgumentsRaw: custom.Custom.Input,
		}, true
	}
	return ToolCall{}, false
}

func chatSystemPrompt(messages []openaiapi.ChatCompletionRequestMessage) string {
	var parts []string
	for _, message := range messages {
		typed, err := message.ValueByDiscriminator()
		if err != nil {
			continue
		}
		switch m := typed.(type) {
		case openaiapi.ChatCompletionRequestSystemMessage:
			parts = append(parts, unionContentString(m.Content))
		case openaiapi.ChatCompletionRequestDeveloperMessage:
			parts = append(parts, unionContentString(m.Content))
		}
	}
	return strings.Join(parts, "\n")
}

func chatTools(items []openaiapi.CreateChatCompletionRequest_Tools_Item) []ToolDecl {
	out := make([]ToolDecl, 0, len(items))
	for _, item := range items {
		raw, err := json.Marshal(item)
		if err != nil {
			continue
		}
		var t openaiapi.ChatCompletionTool
		if json.Unmarshal(raw, &t) == nil && t.Function.Name != "" {
			var params json.RawMessage
			if t.Function.Parameters != nil {
				params, _ = json.Marshal(t.Function.Parameters)
			}
			out = append(out, ToolDecl{Name: t.Function.Name, Type: coalesce(string(t.Type), "function"), SchemaHash: schemaHash(params)})
			continue
		}
		var custom struct {
			Type   string `json:"type"`
			Custom struct {
				Name   string          `json:"name"`
				Format json.RawMessage `json:"format"`
			} `json:"custom"`
			Name string `json:"name"`
		}
		if json.Unmarshal(raw, &custom) == nil {
			name := custom.Custom.Name
			if name == "" {
				name = custom.Name
			}
			out = append(out, ToolDecl{Name: name, Type: coalesce(custom.Type, "custom"), SchemaHash: schemaHash(custom.Custom.Format)})
		}
	}
	return out
}

func chatToolResults(messages []openaiapi.ChatCompletionRequestMessage) []ToolResult {
	var out []ToolResult
	for _, message := range messages {
		tool, err := message.AsChatCompletionRequestToolMessage()
		if err != nil {
			continue
		}
		role, _ := message.Discriminator()
		if role != "tool" {
			continue
		}
		content := unionContentString(tool.Content)
		out = append(out, ToolResult{CallID: tool.ToolCallId, OutputHash: systemHash(content)})
	}
	return out
}

func unionContentString(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return contentString(raw)
}

func unionString(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return ""
}

func coalesce(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
