// SPDX-License-Identifier: AGPL-3.0-only

package lab

import (
	"encoding/json"
	"testing"
	"time"
)

func TestChatStreamToleratesToolDeltasAzureAndOpenRouter(t *testing.T) {
	rawRequest := []byte(`{
		"model":"gpt-test",
		"messages":[{"role":"system","content":"be exact"},{"role":"user","content":"weather?"}],
		"tools":[{"type":"function","function":{"name":"get_weather","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}}]
	}`)
	rawStream := []byte(": OPENROUTER PROCESSING\n\n" +
		"data: {\"id\":\"\",\"object\":\"\",\"model\":\"\",\"created\":0,\"choices\":[],\"prompt_filter_results\":[{\"prompt_index\":0}]}\n\n" +
		"data: {\"id\":\"chatcmpl_1\",\"model\":\"openrouter/auto\",\"provider\":\"openai\",\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"get_weather\",\"arguments\":\"{\\\"city\\\"\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\":\\\"Paris\\\"}\"}}]},\"content_filter_results\":{\"hate\":{\"filtered\":false,\"severity\":\"safe\"}},\"finish_reason\":\"tool_calls\"}],\"usage\":{\"prompt_tokens\":11,\"completion_tokens\":3,\"total_tokens\":14,\"completion_tokens_details\":{\"reasoning_tokens\":1}}}\n\n" +
		"data: [DONE]\n\n")
	call := baseCall("openai.chat", rawRequest, rawStream, true)
	events, complete, warnings := parseSSE(rawStream, call.StartedAt)
	call.SSEEvents = events
	call.Normalized.StreamTruncated = !complete
	call.Normalized.ParserWarnings = warnings
	TolerantParser{}.Parse(&call)

	if got, want := len(call.Normalized.ToolCalls), 1; got != want {
		t.Fatalf("tool calls = %d, want %d", got, want)
	}
	tc := call.Normalized.ToolCalls[0]
	if tc.Name != "get_weather" || string(tc.Arguments) != `{"city":"Paris"}` {
		t.Fatalf("bad tool call: %+v args=%s", tc, string(tc.Arguments))
	}
	if call.Normalized.Usage == nil || deref(call.Normalized.Usage.TotalTokens) != 14 {
		t.Fatalf("usage not parsed: %+v", call.Normalized.Usage)
	}
	if call.Normalized.StreamTruncated {
		t.Fatalf("stream incorrectly marked truncated")
	}
	if call.Normalized.ContentFilter == nil {
		t.Fatalf("content filter chunk not captured")
	}
}

func TestChatStreamCapturesMidStreamError(t *testing.T) {
	rawStream := []byte("data: {\"error\":{\"type\":\"too_many_requests\",\"code\":\"no_capacity\",\"message\":\"try later\"},\"finish_reason\":\"error\"}\n\n")
	call := baseCall("openai.chat", []byte(`{"model":"x"}`), rawStream, true)
	call.SSEEvents, _, _ = parseSSE(rawStream, call.StartedAt)
	TolerantParser{}.Parse(&call)
	if call.Normalized.Error == nil || !call.Normalized.Error.MidStream {
		t.Fatalf("mid-stream error not captured: %+v", call.Normalized.Error)
	}
}

func TestResponsesStreamParsesFunctionCallFromCompletedEvent(t *testing.T) {
	rawRequest := []byte(`{
		"model":"gpt-test",
		"instructions":"use tools",
		"input":"What is the weather?",
		"tools":[{"type":"function","name":"get_weather","parameters":{"type":"object"}}]
	}`)
	terminal := `{"type":"response.completed","response":{"id":"resp_1","model":"gpt-test","status":"completed","usage":{"input_tokens":7,"output_tokens":5,"total_tokens":12,"input_tokens_details":{"cached_tokens":2},"output_tokens_details":{"reasoning_tokens":1}},"output":[{"id":"fc_1","type":"function_call","call_id":"call_1","name":"get_weather","arguments":"{\"city\":\"Paris\"}"}]}}`
	rawStream := []byte("event: response.created\ndata: {\"type\":\"response.created\",\"sequence_number\":0}\n\n" +
		"event: response.completed\ndata: " + terminal + "\n\n")
	call := baseCall("openai.responses", rawRequest, rawStream, true)
	call.SSEEvents, _, _ = parseSSE(rawStream, call.StartedAt)
	TolerantParser{}.Parse(&call)
	if got, want := len(call.Normalized.ToolCalls), 1; got != want {
		t.Fatalf("tool calls = %d, want %d", got, want)
	}
	if call.Normalized.ToolCalls[0].ID != "call_1" || string(call.Normalized.ToolCalls[0].Arguments) != `{"city":"Paris"}` {
		b, _ := json.MarshalIndent(call.Normalized.ToolCalls[0], "", "  ")
		t.Fatalf("bad tool call: %s", b)
	}
	if got := deref(call.Normalized.Usage.CachedInputTokens); got != 2 {
		t.Fatalf("cached tokens = %d, want 2", got)
	}
	if call.Normalized.FinishReason != "tool_calls" {
		t.Fatalf("finish reason = %q", call.Normalized.FinishReason)
	}
	if call.Normalized.StreamTruncated {
		t.Fatalf("responses stream with response.completed marked truncated")
	}
}

func baseCall(family string, rawRequest, rawResponse []byte, stream bool) Call {
	now := time.Now()
	return Call{
		StartedAt:   now,
		EndedAt:     now,
		APIFamily:   family,
		RequestIDs:  map[string]string{},
		RawRequest:  rawRequest,
		RawResponse: rawResponse,
		Stream:      stream,
		HTTPStatus:  200,
	}
}

func deref(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}
