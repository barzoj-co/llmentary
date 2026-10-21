// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"encoding/json"
	"fmt"
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

func TestHTTP401JSONErrorNotMidStream(t *testing.T) {
	body := []byte(`{"error":{"type":"invalid_request_error","code":"invalid_api_key","message":"Incorrect API key provided"}}`)
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream_%v", stream), func(t *testing.T) {
			req := []byte(`{"model":"x"}`)
			if stream {
				req = []byte(`{"model":"x","stream":true}`)
			}
			call := baseCall("openai.chat", req, body, stream)
			call.HTTPStatus = 401
			TolerantParser{}.Parse(&call)
			err := call.Normalized.Error
			if err == nil {
				t.Fatal("expected recorded HTTP error")
			}
			if err.MidStream {
				t.Fatalf("mid_stream=%v want false for 401 JSON: %+v", err.MidStream, err)
			}
			if err.HTTPStatus != 401 {
				t.Fatalf("http_status=%d want 401", err.HTTPStatus)
			}
			if err.Code != "invalid_api_key" {
				t.Fatalf("code=%q want invalid_api_key", err.Code)
			}
			if err.Type != "invalid_request_error" {
				t.Fatalf("type=%q want invalid_request_error", err.Type)
			}
			if err.Normalized != "auth" {
				t.Fatalf("normalized=%q want auth", err.Normalized)
			}
		})
	}
}

func TestHTTP401ChoiceErrorNotMidStream(t *testing.T) {
	body := []byte(`{"choices":[{"error":{"type":"invalid_request_error","code":"invalid_api_key","message":"Incorrect API key provided"}}]}`)
	call := baseCall("openai.chat", []byte(`{"model":"x"}`), body, false)
	call.HTTPStatus = 401
	TolerantParser{}.Parse(&call)
	err := call.Normalized.Error
	if err == nil {
		t.Fatal("expected recorded HTTP error")
	}
	if err.MidStream {
		t.Fatalf("mid_stream=%v want false: %+v", err.MidStream, err)
	}
	if err.HTTPStatus != 401 || err.Code != "invalid_api_key" {
		t.Fatalf("error=%+v", err)
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

func TestChatStreamErrorAfterChunksIsMidStream(t *testing.T) {
	rawStream := []byte("data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n" +
		"data: {\"error\":{\"type\":\"too_many_requests\",\"code\":\"no_capacity\",\"message\":\"try later\"}}\n\n")
	call := baseCall("openai.chat", []byte(`{"model":"x","stream":true}`), rawStream, true)
	call.SSEEvents, _, _ = parseSSE(rawStream, call.StartedAt)
	TolerantParser{}.Parse(&call)
	err := call.Normalized.Error
	if err == nil || !err.MidStream {
		t.Fatalf("mid-stream error after chunks not captured: %+v", err)
	}
	if err.Code != "no_capacity" || err.Type != "too_many_requests" {
		t.Fatalf("error=%+v", err)
	}
	if call.Normalized.OutputText != "partial" {
		t.Fatalf("output_text=%q want partial", call.Normalized.OutputText)
	}
}

func TestMidStreamErrorAcceptsNumericCode(t *testing.T) {
	rawStream := []byte("data: {\"error\":{\"type\":\"rate_limit_error\",\"code\":429,\"message\":\"rate limited\"}}\n\n")
	call := baseCall("openai.chat", []byte(`{"model":"x"}`), rawStream, true)
	call.SSEEvents, _, _ = parseSSE(rawStream, call.StartedAt)
	TolerantParser{}.Parse(&call)
	if call.Normalized.Error == nil || !call.Normalized.Error.MidStream {
		t.Fatalf("numeric-code mid-stream error not captured: %+v", call.Normalized.Error)
	}
	if call.Normalized.Error.Code != "429" || call.Normalized.Error.Message != "rate limited" {
		t.Fatalf("error=%+v", call.Normalized.Error)
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

func TestParseSSEUnterminatedTerminalIsNotComplete(t *testing.T) {
	_, complete, _ := parseSSE([]byte("event: response.completed\ndata: {\"type\":\"response.completed\"}"), time.Now())
	if complete {
		t.Fatal("EOF flush of an unterminated terminal frame must not mark complete")
	}
	_, complete, _ = parseSSE([]byte("data: [DONE]"), time.Now())
	if complete {
		t.Fatal("EOF flush of unterminated [DONE] must not mark complete")
	}
}

func TestParseSSEDelimitedTerminalIsComplete(t *testing.T) {
	_, complete, _ := parseSSE([]byte("event: response.failed\ndata: {\"type\":\"response.failed\"}\n\n"), time.Now())
	if !complete {
		t.Fatal("delimited response.failed must mark complete")
	}
	_, complete, _ = parseSSE([]byte("event: response.incomplete\ndata: {\"type\":\"response.incomplete\"}\n\n"), time.Now())
	if !complete {
		t.Fatal("delimited response.incomplete must mark complete")
	}
}

func TestSSERecorderDoesNotTreatUnterminatedFrameAsComplete(t *testing.T) {
	rec := newSSERecorder(time.Now())
	rec.Write([]byte("event: response.completed\ndata: {\"type\":\"response.completed\"}"))
	_, complete, _ := rec.Finish()
	if complete {
		t.Fatal("unterminated pending frame must not mark stream complete")
	}
}

func TestSSERecorderDelimitedTerminalMarksComplete(t *testing.T) {
	rec := newSSERecorder(time.Now())
	rec.Write([]byte("event: response.completed\ndata: {\"type\":\"response.completed\"}\n\n"))
	_, complete, _ := rec.Finish()
	if !complete {
		t.Fatal("delimited terminal event must mark stream complete")
	}
}

func TestSSERecorderUnterminatedDoneIsNotComplete(t *testing.T) {
	rec := newSSERecorder(time.Now())
	rec.Write([]byte("data: [DONE]"))
	_, complete, _ := rec.Finish()
	if complete {
		t.Fatal("unterminated [DONE] must not mark stream complete")
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
