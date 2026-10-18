// SPDX-License-Identifier: AGPL-3.0-only

package lab

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type chatRequest struct {
	Model    string `json:"model"`
	Messages []struct {
		Role       string          `json:"role"`
		Content    json.RawMessage `json:"content"`
		ToolCallID string          `json:"tool_call_id"`
		Name       string          `json:"name"`
	} `json:"messages"`
	Tools []json.RawMessage `json:"tools"`
}

func parseChat(call *Call) {
	var req chatRequest
	if err := json.Unmarshal(call.RawRequest, &req); err == nil {
		call.Normalized.Model.Requested = req.Model
		call.Normalized.MessagesCount = len(req.Messages)
		call.Normalized.SystemPrompt = chatSystemPrompt(req.Messages)
		call.Normalized.SystemPromptHash = systemHash(call.Normalized.SystemPrompt)
		call.Normalized.ToolsDeclared = chatTools(req.Tools)
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
	body := call.RawResponse
	var resp struct {
		ID       string          `json:"id"`
		Model    string          `json:"model"`
		Usage    json.RawMessage `json:"usage"`
		Provider string          `json:"provider"`
		Choices  []struct {
			FinishReason       string `json:"finish_reason"`
			NativeFinishReason string `json:"native_finish_reason"`
			Message            struct {
				Content          string `json:"content"`
				Reasoning        string `json:"reasoning"`
				ReasoningContent string `json:"reasoning_content"`
				ToolCalls        []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
			Error                any             `json:"error"`
			ContentFilterResults json.RawMessage `json:"content_filter_results"`
		} `json:"choices"`
		Error any `json:"error"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		call.Normalized.ParserWarnings = append(call.Normalized.ParserWarnings, "chat response parse error: "+err.Error())
		return
	}
	if resp.ID != "" {
		call.RequestIDs["body.id"] = resp.ID
	}
	call.Normalized.Model.Returned = resp.Model
	call.Normalized.ServedBy = resp.Provider
	call.Normalized.Usage = usageFromChat(resp.Usage, "body")
	var calls []ToolCall
	for _, choice := range resp.Choices {
		if choice.FinishReason != "" {
			call.Normalized.FinishReason = choice.FinishReason
		}
		if choice.NativeFinishReason != "" {
			call.Normalized.NativeFinishReason = choice.NativeFinishReason
		}
		if choice.Message.Content != "" {
			call.Normalized.OutputText += choice.Message.Content
		}
		if choice.Message.Reasoning != "" || choice.Message.ReasoningContent != "" {
			text := choice.Message.Reasoning + choice.Message.ReasoningContent
			call.Normalized.Reasoning = &Reasoning{Present: true, Chars: len(text)}
		}
		for _, tc := range choice.Message.ToolCalls {
			args, argErr := parseJSONArguments(tc.Function.Arguments)
			calls = append(calls, ToolCall{
				Index: len(calls), ID: tc.ID, Name: tc.Function.Name, Kind: coalesce(tc.Type, "function"),
				ArgumentsRaw: tc.Function.Arguments, Arguments: args, ArgsParseError: argErr,
			})
		}
		if choice.Error != nil && call.Normalized.Error == nil {
			raw, _ := json.Marshal(map[string]any{"error": choice.Error})
			call.Normalized.Error = midStreamError(call.HTTPStatus, raw)
		}
		collectFilter(call, choice.ContentFilterResults)
	}
	call.Normalized.ToolCalls = calls
	if resp.Error != nil && call.Normalized.Error == nil {
		raw, _ := json.Marshal(map[string]any{"error": resp.Error})
		call.Normalized.Error = midStreamError(call.HTTPStatus, raw)
	}
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
	complete := false
	for _, event := range call.SSEEvents {
		if event.Comment != "" || len(bytes.TrimSpace(event.Data)) == 0 {
			continue
		}
		if bytes.Equal(bytes.TrimSpace(event.Data), []byte("[DONE]")) {
			complete = true
			continue
		}
		if err := midStreamError(call.HTTPStatus, event.Data); err != nil {
			call.Normalized.Error = err
		}
		var chunk struct {
			ID           string          `json:"id"`
			Model        string          `json:"model"`
			Provider     string          `json:"provider"`
			Usage        json.RawMessage `json:"usage"`
			Error        any             `json:"error"`
			FinishReason string          `json:"finish_reason"`
			Choices      []struct {
				FinishReason       string `json:"finish_reason"`
				NativeFinishReason string `json:"native_finish_reason"`
				Error              any    `json:"error"`
				Delta              struct {
					Content          string `json:"content"`
					Reasoning        string `json:"reasoning"`
					ReasoningContent string `json:"reasoning_content"`
					ToolCalls        []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Type     string `json:"type"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				ContentFilterResults json.RawMessage `json:"content_filter_results"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(event.Data, &chunk); err != nil {
			call.Normalized.ParserWarnings = append(call.Normalized.ParserWarnings, "chat stream chunk parse error: "+err.Error())
			continue
		}
		if chunk.ID != "" {
			call.RequestIDs["body.id"] = chunk.ID
		}
		if chunk.Model != "" {
			call.Normalized.Model.Returned = chunk.Model
		}
		if chunk.Provider != "" {
			call.Normalized.ServedBy = chunk.Provider
		}
		if len(chunk.Usage) > 0 && !bytes.Equal(bytes.TrimSpace(chunk.Usage), []byte("null")) {
			call.Normalized.Usage = usageFromChat(chunk.Usage, "final_chunk")
		}
		if chunk.Error != nil && call.Normalized.Error == nil {
			raw, _ := json.Marshal(map[string]any{"error": chunk.Error})
			call.Normalized.Error = midStreamError(call.HTTPStatus, raw)
		}
		if chunk.FinishReason != "" {
			call.Normalized.FinishReason = chunk.FinishReason
		}
		for _, choice := range chunk.Choices {
			if choice.FinishReason != "" {
				call.Normalized.FinishReason = choice.FinishReason
			}
			if choice.NativeFinishReason != "" {
				call.Normalized.NativeFinishReason = choice.NativeFinishReason
			}
			if choice.Error != nil && call.Normalized.Error == nil {
				raw, _ := json.Marshal(map[string]any{"error": choice.Error})
				call.Normalized.Error = midStreamError(call.HTTPStatus, raw)
			}
			if choice.Delta.Content != "" {
				call.Normalized.OutputText += choice.Delta.Content
			}
			reasoning := choice.Delta.Reasoning + choice.Delta.ReasoningContent
			if reasoning != "" {
				if call.Normalized.Reasoning == nil {
					call.Normalized.Reasoning = &Reasoning{Present: true}
				}
				call.Normalized.Reasoning.Chars += len(reasoning)
			}
			for _, delta := range choice.Delta.ToolCalls {
				item := accs[delta.Index]
				if item == nil {
					item = &acc{index: delta.Index, kind: "function"}
					accs[delta.Index] = item
				}
				if delta.ID != "" {
					item.id = delta.ID
				}
				if delta.Type != "" {
					item.kind = delta.Type
				}
				if delta.Function.Name != "" {
					item.name = delta.Function.Name
				}
				item.args.WriteString(delta.Function.Arguments)
			}
			collectFilter(call, choice.ContentFilterResults)
		}
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

func chatSystemPrompt(messages []struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	ToolCallID string          `json:"tool_call_id"`
	Name       string          `json:"name"`
}) string {
	var parts []string
	for _, message := range messages {
		if message.Role == "system" || message.Role == "developer" {
			parts = append(parts, contentString(message.Content))
		}
	}
	return strings.Join(parts, "\n")
}

func chatTools(rawTools []json.RawMessage) []ToolDecl {
	out := make([]ToolDecl, 0, len(rawTools))
	for _, raw := range rawTools {
		var t struct {
			Type     string `json:"type"`
			Function struct {
				Name       string          `json:"name"`
				Parameters json.RawMessage `json:"parameters"`
			} `json:"function"`
		}
		if json.Unmarshal(raw, &t) != nil {
			continue
		}
		out = append(out, ToolDecl{Name: t.Function.Name, Type: coalesce(t.Type, "function"), SchemaHash: schemaHash(t.Function.Parameters)})
	}
	return out
}

func chatToolResults(messages []struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	ToolCallID string          `json:"tool_call_id"`
	Name       string          `json:"name"`
}) []ToolResult {
	var out []ToolResult
	for _, message := range messages {
		if message.Role != "tool" {
			continue
		}
		content := contentString(message.Content)
		out = append(out, ToolResult{CallID: message.ToolCallID, Name: message.Name, OutputHash: systemHash(content)})
	}
	return out
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

func coalesce(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
