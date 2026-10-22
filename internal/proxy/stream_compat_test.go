// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"strings"
	"testing"
)

func parseChatSSE(t *testing.T, stream string) Call {
	t.Helper()
	raw := []byte(stream)
	call := baseCall("openai.chat", []byte(`{"model":"x","stream":true}`), raw, true)
	call.SSEEvents, _, _ = parseSSE(raw, call.StartedAt)
	TolerantParser{}.Parse(&call)
	return call
}

func parseChatJSON(t *testing.T, body string) Call {
	t.Helper()
	call := baseCall("openai.chat", []byte(`{"model":"x"}`), []byte(body), false)
	TolerantParser{}.Parse(&call)
	return call
}

func hasWarning(call Call, sub string) bool {
	for _, w := range call.Normalized.ParserWarnings {
		if strings.Contains(w, sub) {
			return true
		}
	}
	return false
}

func TestNormalizeErrorCompatClasses(t *testing.T) {
	t.Parallel()
	tests := []struct {
		status int
		code   string
		typ    string
		want   string
	}{
		{200, "server_error", "", "server"},
		{200, "", "server_error", "server"},
		{200, "", "internal_server_error", "server"},
		{200, "500", "InternalServerError", "server"},
		{200, "503", "", "server"},
		{200, "429", "", "rate_limit"},
		{200, "", "rate_limit_error", "rate_limit"},
		{200, "no_capacity", "too_many_requests", "rate_limit"},
		{429, "", "", "rate_limit"},
		{500, "", "", "server"},
		{200, "content_filter", "", "content_filter"},
		{200, "unknown_vendor", "weird", "unknown"},
		{401, "", "", "auth"},
		{400, "invalid", "invalid_request_error", "bad_request"},
	}
	for _, tc := range tests {
		t.Run(tc.code+"_"+tc.typ, func(t *testing.T) {
			t.Parallel()
			if got := normalizeError(tc.status, tc.code, tc.typ); got != tc.want {
				t.Fatalf("normalizeError(%d,%q,%q)=%q want %q", tc.status, tc.code, tc.typ, got, tc.want)
			}
		})
	}
}

func TestMapChatFinishReason(t *testing.T) {
	t.Parallel()
	tests := []struct {
		raw, norm, native string
		known             bool
	}{
		{"stop", "stop", "", true},
		{"eos", "stop", "eos", true},
		{"EOS", "stop", "EOS", true},
		{"error", "error", "error", true},
		{"tool_calls", "tool_calls", "", true},
		{"length", "length", "", true},
		{"content_filter", "content_filter", "", true},
		{"function_call", "function_call", "", true},
		{"banana", "unknown", "banana", false},
	}
	for _, tc := range tests {
		t.Run(tc.raw, func(t *testing.T) {
			norm, native, known := mapChatFinishReason(tc.raw)
			if norm != tc.norm || native != tc.native || known != tc.known {
				t.Fatalf("got %q %q %v want %q %q %v", norm, native, known, tc.norm, tc.native, tc.known)
			}
		})
	}
}

func TestOpenRouterMidStreamServerErrorSSE(t *testing.T) {
	stream := "data: {\"error\":{\"code\":\"server_error\",\"message\":\"Provider disconnected unexpectedly\"},\"choices\":[{\"finish_reason\":\"error\",\"native_finish_reason\":\"stop\"}]}\n\n"
	call := parseChatSSE(t, stream)
	if call.Normalized.Error == nil || !call.Normalized.Error.MidStream {
		t.Fatalf("error=%+v", call.Normalized.Error)
	}
	if call.Normalized.Error.Normalized != "server" || call.Normalized.Error.Code != "server_error" {
		t.Fatalf("error=%+v", call.Normalized.Error)
	}
	if call.Normalized.FinishReason != "error" {
		t.Fatalf("finish=%q", call.Normalized.FinishReason)
	}
	if call.Normalized.NativeFinishReason != "stop" {
		t.Fatalf("native=%q", call.Normalized.NativeFinishReason)
	}
	if !call.Normalized.StreamTruncated {
		t.Fatal("error-only stream without [DONE] should stay truncated")
	}
}

func TestOpenRouterMidStreamServerErrorJSON(t *testing.T) {
	body := `{"id":"c","error":{"code":"server_error","message":"Provider disconnected unexpectedly"},"choices":[{"index":0,"message":{"role":"assistant","content":""},"finish_reason":"error"}]}`
	call := parseChatJSON(t, body)
	if call.Normalized.Error == nil || !call.Normalized.Error.MidStream {
		t.Fatalf("error=%+v", call.Normalized.Error)
	}
	if call.Normalized.Error.Normalized != "server" {
		t.Fatalf("normalized=%q", call.Normalized.Error.Normalized)
	}
	if call.Normalized.FinishReason != "error" || call.Normalized.NativeFinishReason != "error" {
		t.Fatalf("finish=%q native=%q", call.Normalized.FinishReason, call.Normalized.NativeFinishReason)
	}
}

func TestVLLMInternalServerErrorSSEAndJSON(t *testing.T) {
	sse := "data: {\"error\":{\"object\":\"error\",\"type\":\"InternalServerError\",\"code\":500,\"message\":\"boom\"}}\n\n"
	call := parseChatSSE(t, sse)
	if call.Normalized.Error == nil || !call.Normalized.Error.MidStream || call.Normalized.Error.Normalized != "server" {
		t.Fatalf("sse error=%+v", call.Normalized.Error)
	}
	if call.Normalized.Error.Code != "500" {
		t.Fatalf("code=%q", call.Normalized.Error.Code)
	}

	jsonCall := parseChatJSON(t, `{"error":{"object":"error","type":"InternalServerError","code":"500","message":"boom"}}`)
	if jsonCall.Normalized.Error == nil || !jsonCall.Normalized.Error.MidStream || jsonCall.Normalized.Error.Normalized != "server" {
		t.Fatalf("json error=%+v", jsonCall.Normalized.Error)
	}
}

func TestMidStreamErrorFirstOnlyThenAfterContentAndDone(t *testing.T) {
	t.Run("after_partial", func(t *testing.T) {
		stream := "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n" +
			"data: {\"error\":{\"code\":\"server_error\",\"message\":\"gone\"}}\n\n"
		call := parseChatSSE(t, stream)
		if call.Normalized.OutputText != "hello" {
			t.Fatalf("text=%q", call.Normalized.OutputText)
		}
		if call.Normalized.Error == nil || call.Normalized.Error.Normalized != "server" || !call.Normalized.Error.MidStream {
			t.Fatalf("error=%+v", call.Normalized.Error)
		}
	})
	t.Run("error_then_done", func(t *testing.T) {
		stream := "data: {\"error\":{\"type\":\"InternalServerError\",\"code\":500,\"message\":\"boom\"}}\n\n" +
			"data: [DONE]\n\n"
		call := parseChatSSE(t, stream)
		if call.Normalized.Error == nil || call.Normalized.Error.Normalized != "server" {
			t.Fatalf("error=%+v", call.Normalized.Error)
		}
		if call.Normalized.StreamTruncated {
			t.Fatal("error+[DONE] should not be truncated")
		}
	})
	t.Run("rate_limit_429", func(t *testing.T) {
		stream := "data: {\"error\":{\"type\":\"rate_limit_error\",\"code\":429,\"message\":\"slow down\"}}\n\n"
		call := parseChatSSE(t, stream)
		if call.Normalized.Error == nil || call.Normalized.Error.Normalized != "rate_limit" {
			t.Fatalf("error=%+v", call.Normalized.Error)
		}
	})
}

func TestStreamCommentsAndCRLF(t *testing.T) {
	stream := ": OPENROUTER PROCESSING\r\n\r\n" +
		": keep-alive\r\n\r\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\r\n\r\n" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\r\n\r\n" +
		"data: [DONE]\r\n\r\n"
	call := parseChatSSE(t, stream)
	if call.Normalized.OutputText != "hi" {
		t.Fatalf("text=%q", call.Normalized.OutputText)
	}
	if call.Normalized.FinishReason != "stop" {
		t.Fatalf("finish=%q", call.Normalized.FinishReason)
	}
	if call.Normalized.StreamTruncated {
		t.Fatal("truncated")
	}
	if call.Normalized.Usage == nil || deref(call.Normalized.Usage.TotalTokens) != 2 {
		t.Fatalf("usage=%+v", call.Normalized.Usage)
	}
}

func TestOpenRouterRepeatingFinishReasonNotDoubleCounted(t *testing.T) {
	stream := "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"},\"finish_reason\":\"error\"}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"error\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2,\"cost\":0.001}}\n\n" +
		"data: [DONE]\n\n"
	call := parseChatSSE(t, stream)
	if call.Normalized.FinishReason != "error" {
		t.Fatalf("finish=%q", call.Normalized.FinishReason)
	}
	count := 0
	for _, w := range call.Normalized.ParserWarnings {
		if strings.Contains(w, warnUnknownFinish) {
			count++
		}
	}
	if count != 0 {
		t.Fatalf("mapped error should not warn, warnings=%v", call.Normalized.ParserWarnings)
	}
	if call.Normalized.Usage == nil || call.Normalized.Usage.CostUSD == nil || *call.Normalized.Usage.CostUSD != 0.001 {
		t.Fatalf("usage=%+v", call.Normalized.Usage)
	}
}

func TestNoDoneMarkerWhenFinishAndUsage(t *testing.T) {
	stream := "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":1,\"total_tokens\":3}}\n\n"
	call := parseChatSSE(t, stream)
	if call.Normalized.StreamTruncated {
		t.Fatal("finish+usage without [DONE] should not truncate")
	}
	if !hasWarning(call, warnNoDoneMarker) {
		t.Fatalf("warnings=%v want %s", call.Normalized.ParserWarnings, warnNoDoneMarker)
	}
	if call.Normalized.FinishReason != "stop" {
		t.Fatalf("finish=%q", call.Normalized.FinishReason)
	}
}

func TestNoDoneStillTruncatedWithoutUsageOrFinish(t *testing.T) {
	t.Run("finish_no_usage", func(t *testing.T) {
		call := parseChatSSE(t, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n")
		if !call.Normalized.StreamTruncated {
			t.Fatal("finish without usage should stay truncated")
		}
		if hasWarning(call, warnNoDoneMarker) {
			t.Fatalf("unexpected no_done_marker: %v", call.Normalized.ParserWarnings)
		}
	})
	t.Run("usage_no_finish", func(t *testing.T) {
		call := parseChatSSE(t, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n\n")
		if !call.Normalized.StreamTruncated {
			t.Fatal("usage without finish should stay truncated")
		}
	})
}

func TestGroqXGroqUsageFallback(t *testing.T) {
	t.Run("sse_x_groq_only", func(t *testing.T) {
		stream := "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n" +
			"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"x_groq\":{\"usage\":{\"prompt_tokens\":4,\"completion_tokens\":2,\"total_tokens\":6}}}\n\n" +
			"data: [DONE]\n\n"
		call := parseChatSSE(t, stream)
		if call.Normalized.Usage == nil || call.Normalized.Usage.Source != "x_groq" {
			t.Fatalf("usage=%+v", call.Normalized.Usage)
		}
		if deref(call.Normalized.Usage.TotalTokens) != 6 {
			t.Fatalf("total=%v", call.Normalized.Usage.TotalTokens)
		}
	})
	t.Run("top_level_wins", func(t *testing.T) {
		stream := "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2},\"x_groq\":{\"usage\":{\"prompt_tokens\":9,\"completion_tokens\":9,\"total_tokens\":18}}}\n\n" +
			"data: [DONE]\n\n"
		call := parseChatSSE(t, stream)
		if call.Normalized.Usage == nil || call.Normalized.Usage.Source != "final_chunk" {
			t.Fatalf("usage=%+v", call.Normalized.Usage)
		}
		if deref(call.Normalized.Usage.TotalTokens) != 2 {
			t.Fatalf("total=%v want top-level 2", call.Normalized.Usage.TotalTokens)
		}
	})
	t.Run("json_x_groq", func(t *testing.T) {
		body := `{"id":"c","model":"llama","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"x_groq":{"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}}`
		call := parseChatJSON(t, body)
		if call.Normalized.Usage == nil || call.Normalized.Usage.Source != "x_groq" || deref(call.Normalized.Usage.TotalTokens) != 4 {
			t.Fatalf("usage=%+v", call.Normalized.Usage)
		}
	})
}

func TestTogetherEOSJSONAndSSE(t *testing.T) {
	jsonCall := parseChatJSON(t, `{"id":"c","model":"tog","choices":[{"index":0,"message":{"role":"assistant","content":"done"},"finish_reason":"eos"}]}`)
	if jsonCall.Normalized.FinishReason != "stop" || jsonCall.Normalized.NativeFinishReason != "eos" {
		t.Fatalf("json finish=%q native=%q", jsonCall.Normalized.FinishReason, jsonCall.Normalized.NativeFinishReason)
	}
	if hasWarning(jsonCall, warnUnknownFinish) {
		t.Fatalf("warnings=%v", jsonCall.Normalized.ParserWarnings)
	}

	sse := parseChatSSE(t, "data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"eos\"}]}\n\ndata: [DONE]\n\n")
	if sse.Normalized.FinishReason != "stop" || sse.Normalized.NativeFinishReason != "eos" {
		t.Fatalf("sse finish=%q native=%q", sse.Normalized.FinishReason, sse.Normalized.NativeFinishReason)
	}
}

func TestUnknownFinishReasonKeepsRaw(t *testing.T) {
	call := parseChatJSON(t, `{"id":"c","choices":[{"index":0,"message":{"role":"assistant","content":"x"},"finish_reason":"banana"}]}`)
	if call.Normalized.FinishReason != "unknown" || call.Normalized.NativeFinishReason != "banana" {
		t.Fatalf("finish=%q native=%q", call.Normalized.FinishReason, call.Normalized.NativeFinishReason)
	}
	if !hasWarning(call, warnUnknownFinish+": banana") {
		t.Fatalf("warnings=%v", call.Normalized.ParserWarnings)
	}
}

func TestResponsesStreamUnchangedByChatDoneHardening(t *testing.T) {
	// Responses complete on response.completed, not [DONE] or chat finish+usage.
	terminal := `{"type":"response.completed","response":{"id":"resp_1","model":"gpt-test","status":"completed","output":[{"id":"msg","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok","annotations":[],"logprobs":[]}]}]}}`
	raw := []byte("event: response.completed\ndata: " + terminal + "\n\n")
	call := baseCall("openai.responses", []byte(`{"model":"x","input":"hi"}`), raw, true)
	call.SSEEvents, _, _ = parseSSE(raw, call.StartedAt)
	TolerantParser{}.Parse(&call)
	if call.Normalized.StreamTruncated {
		t.Fatal("responses completed stream marked truncated")
	}
	if hasWarning(call, warnNoDoneMarker) {
		t.Fatalf("responses should not get no_done_marker: %v", call.Normalized.ParserWarnings)
	}
	if call.Normalized.OutputText != "ok" || call.Normalized.FinishReason != "stop" {
		t.Fatalf("text=%q finish=%q", call.Normalized.OutputText, call.Normalized.FinishReason)
	}
}
