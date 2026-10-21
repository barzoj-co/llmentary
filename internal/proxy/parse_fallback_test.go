// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestApplyResponsesFallbackParsesOutputAndUsage(t *testing.T) {
	t.Parallel()
	call := Call{RequestIDs: map[string]string{}, HTTPStatus: 200}
	raw := []byte(`{
		"id":"resp_fb",
		"model":"gpt-test",
		"status":"incomplete",
		"incomplete_details":{"reason":"max_output_tokens"},
		"output_text":"override",
		"usage":{"input_tokens":3,"output_tokens":4,"total_tokens":7,
			"prompt_tokens_details":{"cached_tokens":1,"audio_tokens":2},
			"completion_tokens_details":{"reasoning_tokens":1,"accepted_prediction_tokens":1,"rejected_prediction_tokens":0,"audio_tokens":9}},
		"output":[
			{"type":"function_call","call_id":"c1","id":"fc_1","name":"lookup","arguments":"{\"q\":1}"},
			{"type":"custom_tool_call","call_id":"c2","id":"ct_1","name":"custom","input":"not-json"},
			{"type":"message","content":[
				{"type":"output_text","text":"hello"},
				{"type":"refusal","refusal":"nope"},
				{"type":"other","text":"ignored"}
			]},
			{"type":"message","content":"not-an-array"}
		]
	}`)
	applyResponsesFallback(&call, raw, warnStrictDecodeFailed, "body")
	if call.RequestIDs["body.id"] != "resp_fb" {
		t.Fatalf("id=%v", call.RequestIDs)
	}
	if call.Normalized.Model.Returned != "gpt-test" {
		t.Fatalf("model=%q", call.Normalized.Model.Returned)
	}
	if call.Normalized.OutputText != "override" {
		t.Fatalf("output=%q", call.Normalized.OutputText)
	}
	if call.Normalized.Refusal != "nope" {
		t.Fatalf("refusal=%q", call.Normalized.Refusal)
	}
	if call.Normalized.FinishReason == "" || call.Normalized.NativeFinishReason != "incomplete" {
		t.Fatalf("finish=%q native=%q", call.Normalized.FinishReason, call.Normalized.NativeFinishReason)
	}
	if call.Normalized.Usage == nil || deref(call.Normalized.Usage.TotalTokens) != 7 {
		t.Fatalf("usage=%+v", call.Normalized.Usage)
	}
	if len(call.Normalized.ToolCalls) != 2 {
		t.Fatalf("tools=%+v", call.Normalized.ToolCalls)
	}
	if call.Normalized.ToolCalls[0].Kind != "function" || call.Normalized.ToolCalls[1].Kind != "custom" {
		t.Fatalf("kinds=%q %q", call.Normalized.ToolCalls[0].Kind, call.Normalized.ToolCalls[1].Kind)
	}
	if call.Normalized.ToolCalls[1].ArgsParseError == "" {
		t.Fatal("expected custom tool args parse error")
	}
	if !containsString(call.Normalized.ParserWarnings, warnStrictDecodeFailed) {
		t.Fatalf("warnings=%v", call.Normalized.ParserWarnings)
	}
}

func TestApplyResponsesFallbackErrorAndToolFinish(t *testing.T) {
	t.Parallel()
	errCall := Call{RequestIDs: map[string]string{}, HTTPStatus: 400}
	applyResponsesFallback(&errCall, []byte(`{"error":{"type":"invalid_request_error","code":"bad","message":"no"}}`), "fallback", "body")
	if errCall.Normalized.Error == nil || errCall.Normalized.FinishReason != "error" {
		t.Fatalf("error path: %+v", errCall.Normalized)
	}
	if errCall.Normalized.Error.Code != "bad" || errCall.Normalized.Error.Type != "invalid_request_error" {
		t.Fatalf("error=%+v", errCall.Normalized.Error)
	}

	toolCall := Call{RequestIDs: map[string]string{}}
	applyResponsesFallback(&toolCall, []byte(`{"output":[{"type":"function_call","name":"f","arguments":"{}"}]}`), "fallback", "body")
	if toolCall.Normalized.FinishReason != "tool_calls" {
		t.Fatalf("tool finish=%q", toolCall.Normalized.FinishReason)
	}

	applyResponsesFallback(&Call{RequestIDs: map[string]string{}}, []byte(`not-json`), "fallback", "body")
	nullErr := Call{RequestIDs: map[string]string{}}
	applyResponsesFallback(&nullErr, []byte(`{"error":null,"status":"completed"}`), "fallback", "body")
	if nullErr.Normalized.Error != nil {
		t.Fatalf("null error recorded: %+v", nullErr.Normalized.Error)
	}
}

func TestApplyChatFallbackExtractsDeltaAndTools(t *testing.T) {
	t.Parallel()
	call := Call{RequestIDs: map[string]string{}, HTTPStatus: 200}
	raw := []byte(`{
		"id":"chat_fb",
		"model":"m",
		"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5},
		"choices":[
			{"finish_reason":"tool_calls","message":{
				"content":"hi",
				"refusal":"r",
				"tool_calls":[
					{"index":1,"id":"t1","type":"function","function":{"name":"lookup","arguments":"{"}},
					{"id":"t2","function":{"name":"bad","arguments":"not-json"}}
				]
			}},
			{"delta":{"content":"!"},"finish_reason":"stop"},
			{"message":"not-an-object"}
		]
	}`)
	applyChatFallback(&call, raw, warnStrictDecodeFailed)
	if call.RequestIDs["body.id"] != "chat_fb" || call.Normalized.Model.Returned != "m" {
		t.Fatalf("ids/model %+v %q", call.RequestIDs, call.Normalized.Model.Returned)
	}
	if !strings.Contains(call.Normalized.OutputText, "hi") || !strings.Contains(call.Normalized.OutputText, "!") {
		t.Fatalf("content=%q", call.Normalized.OutputText)
	}
	if call.Normalized.Refusal != "r" || call.Normalized.FinishReason != "stop" {
		t.Fatalf("refusal=%q finish=%q", call.Normalized.Refusal, call.Normalized.FinishReason)
	}
	if call.Normalized.Usage == nil || deref(call.Normalized.Usage.TotalTokens) != 5 {
		t.Fatalf("usage=%+v", call.Normalized.Usage)
	}
	if len(call.Normalized.ToolCalls) != 2 {
		t.Fatalf("tools=%+v", call.Normalized.ToolCalls)
	}
	if call.Normalized.ToolCalls[1].ArgsParseError == "" {
		t.Fatal("expected invalid tool args warning")
	}
}

func TestExtractLooseChatInvalidJSON(t *testing.T) {
	t.Parallel()
	if got := extractLooseChat([]byte(`{`)); got.model != "" {
		t.Fatalf("%+v", got)
	}
	if got := extractLooseChat([]byte(`{"model":"x"}`)); got.model != "x" || got.finish != "" {
		t.Fatalf("%+v", got)
	}
	if got := extractLooseChatChunk([]byte(`{"id":"i","choices":[]}`)); got.id != "i" {
		t.Fatalf("%+v", got)
	}
}

func TestUsageFromLooseChatAndResponsesShapes(t *testing.T) {
	t.Parallel()
	chat := usageFromLoose(json.RawMessage(`{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}`), "chat")
	if chat == nil || deref(chat.InputTokens) != 1 || deref(chat.OutputTokens) != 2 {
		t.Fatalf("chat=%+v", chat)
	}
	resp := usageFromLoose(json.RawMessage(`{"input_tokens":4,"output_tokens":5,"total_tokens":9}`), "resp")
	if resp == nil || deref(resp.InputTokens) != 4 || deref(resp.OutputTokens) != 5 {
		t.Fatalf("resp=%+v", resp)
	}
	if usageFromLoose(json.RawMessage(`[]`), "x") != nil {
		t.Fatal("array usage should be nil")
	}
	if usageFromLoose(json.RawMessage(`{"cost":1.5}`), "x") != nil {
		t.Fatal("cost-only usage should be nil")
	}
	mixed := usageFromLoose(json.RawMessage(`{
		"prompt_tokens":"6",
		"completion_tokens":"7",
		"total_tokens":"13",
		"cost":0.25,
		"prompt_tokens_details":{"cached_tokens":"1","audio_tokens":"2"},
		"completion_tokens_details":{"reasoning_tokens":"1","accepted_prediction_tokens":"1","rejected_prediction_tokens":"0","audio_tokens":"9"}
	}`), "loose")
	if mixed == nil || deref(mixed.TotalTokens) != 13 || mixed.CostUSD == nil || *mixed.CostUSD != 0.25 {
		t.Fatalf("mixed=%+v", mixed)
	}
	if deref(mixed.CachedInputTokens) != 1 || deref(mixed.ReasoningTokens) != 1 || deref(mixed.AudioTokens) != 2 {
		t.Fatalf("details=%+v", mixed)
	}
}

func TestRawStringAndCoerceIntCorners(t *testing.T) {
	t.Parallel()
	if rawString(nil) != "" || rawString(json.RawMessage("null")) != "" {
		t.Fatal("empty/null")
	}
	if rawString(json.RawMessage(`"hi"`)) != "hi" {
		t.Fatal("quoted")
	}
	if rawString(json.RawMessage(`unquoted`)) != "unquoted" {
		t.Fatal("unquoted")
	}
	if n, ok := coerceInt(json.RawMessage(`7`)); !ok || *n != 7 {
		t.Fatal("int")
	}
	if n, ok := coerceInt(json.RawMessage(`" 8 "`)); !ok || *n != 8 {
		t.Fatal("string int")
	}
	if _, ok := coerceInt(json.RawMessage(`"x"`)); ok {
		t.Fatal("non-int string")
	}
	if _, ok := coerceInt(json.RawMessage(`null`)); ok {
		t.Fatal("null")
	}
	if _, ok := coerceInt(json.RawMessage(`true`)); ok {
		t.Fatal("bool")
	}
}

func TestWarnUnknownFinishAndMissingRequired(t *testing.T) {
	t.Parallel()
	call := Call{}
	warnUnknownFinishReason(&call, "", false)
	warnUnknownFinishReason(&call, "stop", true)
	warnUnknownFinishReason(&call, "weird", false)
	if len(call.Normalized.ParserWarnings) != 1 || !strings.Contains(call.Normalized.ParserWarnings[0], "weird") {
		t.Fatalf("warnings=%v", call.Normalized.ParserWarnings)
	}
	noteMissingRequired(&call, []byte(`{"model":"m"}`), "model", "messages")
	found := false
	for _, w := range call.Normalized.ParserWarnings {
		if strings.Contains(w, "messages") {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing messages: %v", call.Normalized.ParserWarnings)
	}
}
