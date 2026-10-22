// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestApplyResponsesAndChatExtensionsCorners(t *testing.T) {
	t.Parallel()
	call := Call{}
	applyResponsesExtensions(&call, json.RawMessage(`not-json`))
	applyResponsesExtensions(&call, json.RawMessage(`{"content_filters":[{"hate":{"filtered":true,"severity":"high"}},"skip",{"error":{}}]}`))
	if call.Normalized.ContentFilter == nil || !call.Normalized.ContentFilter.CompletionFiltered {
		t.Fatalf("filters=%+v", call.Normalized.ContentFilter)
	}
	if !call.Normalized.ContentFilter.FilterSystemError {
		t.Fatal("filter system error")
	}

	applyChatExtensions(&call, nil, nil)
	applyChatExtensions(&call, json.RawMessage(`{`), nil)
	applyChatExtensions(&call, json.RawMessage(`{
		"provider":"openrouter",
		"usage":{"cost":1.25},
		"prompt_filter_results":[{"prompt_index":0,"content_filter_results":{"hate":{"filtered":true}}}],
		"choices":[{"native_finish_reason":"stop","finish_reason":"stop","content_filter_results":{"violence":{"filtered":false}},"message":{"reasoning":"think"},"delta":null}, "bad"],
		"error":null
	}`), nil)
	if call.Normalized.ServedBy != "openrouter" {
		t.Fatalf("served=%q", call.Normalized.ServedBy)
	}
	if call.Normalized.Reasoning == nil || call.Normalized.Reasoning.Chars == 0 {
		t.Fatalf("reasoning=%+v", call.Normalized.Reasoning)
	}
	if call.Normalized.ContentFilter == nil || !call.Normalized.ContentFilter.PromptFiltered {
		t.Fatalf("prompt filter=%+v", call.Normalized.ContentFilter)
	}

	errCall := Call{HTTPStatus: 400}
	applyChatExtensions(&errCall, json.RawMessage(`{"error":{"type":"invalid_request_error","code":"bad","message":"no"}}`), nil)
	if errCall.Normalized.Error == nil || errCall.Normalized.Error.MidStream {
		t.Fatalf("http error=%+v", errCall.Normalized.Error)
	}
	mid := Call{HTTPStatus: 200}
	applyChatExtensions(&mid, json.RawMessage(`{"choices":[{"error":{"type":"rate","code":"x","message":"later"}}]}`), nil)
	if mid.Normalized.Error == nil || !mid.Normalized.Error.MidStream {
		t.Fatalf("mid-stream=%+v", mid.Normalized.Error)
	}

	applyUsageExtensions(nil, json.RawMessage(`{"cost":1}`))
	u := &Usage{}
	applyUsageExtensions(u, nil)
	applyReasoningFrom(&Call{}, json.RawMessage(`null`))
	collectFilter(&Call{}, json.RawMessage(`null`))
	collectPromptFilter(&Call{}, json.RawMessage(`null`))
}

func TestSanitizeURLAndParseLooseError(t *testing.T) {
	t.Parallel()
	got := sanitizeURL("https://api.example/v1?api_key=secret&keep=1")
	if got == "https://api.example/v1?api_key=secret&keep=1" || !strings.Contains(got, "sha256") {
		t.Fatalf("secret not hashed: %s", got)
	}
	withUser := sanitizeInFlightURL("https://user:hunter2@api.example/v1?api_key=secret")
	if strings.Contains(withUser, "user") || strings.Contains(withUser, "hunter2") || strings.Contains(withUser, "@") {
		t.Fatalf("userinfo leaked: %s", withUser)
	}
	if !strings.Contains(withUser, "sha256") {
		t.Fatalf("query secret not hashed: %s", withUser)
	}
	if sanitizeInFlightURL("https://justuser@api.example/v1") != "https://api.example/v1" {
		t.Fatalf("username-only userinfo: %s", sanitizeInFlightURL("https://justuser@api.example/v1"))
	}
	if got := sanitizeURL("https://user:hunter2@api.example/v1"); !strings.Contains(got, "user") {
		t.Fatalf("sanitizeURL keeps userinfo for /config redaction path: %s", got)
	}
	_ = sanitizeInFlightURL("http://[::1")
	_ = sanitizeURL("%")
	typ, code, msg, ok := parseLooseError(json.RawMessage(`{"type":"t","code":429,"message":"m"}`))
	if !ok || typ != "t" || code != "429" || msg != "m" {
		t.Fatalf("%s %s %s %v", typ, code, msg, ok)
	}
	if _, _, _, ok := parseLooseError(json.RawMessage(`{"foo":1}`)); ok {
		t.Fatal("empty error")
	}
	if _, _, _, ok := parseLooseError(json.RawMessage(`[]`)); ok {
		t.Fatal("array")
	}
	if looseErrorCode(json.RawMessage(`null`)) != "" || looseErrorCode(json.RawMessage(`"c"`)) != "c" {
		t.Fatal("loose code")
	}
	if normalizeError(401, "", "") != "auth" || normalizeError(429, "", "") != "rate_limit" {
		t.Fatal("normalize status")
	}
	if normalizeError(200, "overloaded", "") != "server" || normalizeError(200, "content_filter", "") != "content_filter" {
		t.Fatal("normalize text")
	}
	if normalizeError(200, "", "") != "unknown" || normalizeError(400, "", "") != "bad_request" {
		t.Fatal("normalize default")
	}
}

func TestRequestStreamFlagAndFirstJSONBytes(t *testing.T) {
	t.Parallel()
	if !requestStreamFlag([]byte(`{"stream":true}`)) {
		t.Fatal("true")
	}
	if requestStreamFlag([]byte(`{"stream":false}`)) || requestStreamFlag([]byte(`{"stream":"yes"}`)) {
		t.Fatal("non-true stream")
	}
	if requestStreamFlag([]byte(`not-json`)) {
		t.Fatal("invalid")
	}
	if string(firstJSONBytes([]byte("not-json"))) != "not-json" {
		t.Fatal("passthrough")
	}
	if string(firstJSONBytes([]byte("{\"a\":1} trailing"))) != `{"a":1}` {
		t.Fatal("first object")
	}
}
