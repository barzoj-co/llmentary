// SPDX-License-Identifier: AGPL-3.0-only

package proxy_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/barzoj-co/llmentary/internal/proxy"
)

func chatSSEFrames(done bool, frames ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for _, frame := range frames {
			if frame == "" {
				_, _ = w.Write([]byte(":\n\n"))
				continue
			}
			_, _ = fmt.Fprintf(w, "data: %s\n\n", frame)
		}
		if done {
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		}
	}
}

const (
	chatDeltaPartial = `{"id":"chatcmpl_error","object":"chat.completion.chunk","created":1,"model":"gpt-fixture","choices":[{"index":0,"delta":{"content":"partial"},"finish_reason":null}]}`
	chatErrorNested  = `{"error":{"type":"rate_limit_error","code":"rate_limit_exceeded","message":"slow down"}}`
	chatErrorFixture = `{"error":{"type":"rate_limit_error","code":"rate_limit_exceeded","message":"slow down"},"finish_reason":"error"}`
	chatErrorNumeric = `{"error":{"type":"server_error","code":429,"message":"rate limited"}}`
)

func TestMidStreamErrorSurvivesResponseShape(t *testing.T) {
	cases := []struct {
		name    string
		mode    string
		opt     func(*proxy.Config)
		handler http.HandlerFunc
		code    string
	}{
		{
			name:    "compatible_after_content_no_done",
			mode:    "warn",
			opt:     withCompatTarget(),
			handler: chatSSEFrames(false, chatDeltaPartial, chatErrorNested),
			code:    "rate_limit_exceeded",
		},
		{
			name:    "openai_after_content_no_done",
			mode:    "warn",
			opt:     withOpenAITarget(),
			handler: chatSSEFrames(false, chatDeltaPartial, chatErrorNested),
			code:    "rate_limit_exceeded",
		},
		{
			name:    "compatible_fixture_toplevel_finish_reason",
			mode:    "warn",
			opt:     withCompatTarget(),
			handler: chatSSEFrames(false, chatDeltaPartial, chatErrorFixture),
			code:    "rate_limit_exceeded",
		},
		{
			name:    "compatible_error_first",
			mode:    "warn",
			opt:     withCompatTarget(),
			handler: chatSSEFrames(false, chatErrorNested),
			code:    "rate_limit_exceeded",
		},
		{
			name:    "compatible_blank_then_error",
			mode:    "warn",
			opt:     withCompatTarget(),
			handler: chatSSEFrames(false, "", chatDeltaPartial, "", chatErrorNested),
			code:    "rate_limit_exceeded",
		},
		{
			name:    "compatible_numeric_code_then_done",
			mode:    "warn",
			opt:     withCompatTarget(),
			handler: chatSSEFrames(true, chatDeltaPartial, chatErrorNumeric),
			code:    "429",
		},
		{
			name:    "compatible_enforce_does_not_drop_error",
			mode:    "enforce",
			opt:     withCompatTarget(),
			handler: chatSSEFrames(false, chatDeltaPartial, chatErrorFixture),
			code:    "rate_limit_exceeded",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logBuf := &syncLogBuf{}
			p := startSpecProxy(t, tc.mode, "relaxed", tc.handler, tc.opt, withTestLogger(logBuf))
			sid := newSession()
			status, _, raw, sess := postRaw(t, p, "/v1/chat/completions", sid, nil, chatStreamBody)
			if status != http.StatusOK {
				t.Fatalf("status=%d body=%s", status, raw)
			}
			call := lastCall(t, sess)
			errObj, _ := normalized(call)["error"].(map[string]any)
			if errObj == nil || errObj["mid_stream"] != true {
				t.Fatalf("mid_stream error lost: error=%v call=%v", errObj, call)
			}
			if fmtString(errObj["code"]) != tc.code {
				t.Fatalf("code=%v want %s", errObj["code"], tc.code)
			}
			if fmtString(errObj["source"]) != "upstream" {
				t.Fatalf("source=%v", errObj)
			}
			if asBool(normalized(call)["client_aborted"]) {
				t.Fatalf("drained stream must not abort: %v", normalized(call))
			}
			for _, v := range specViolations(call) {
				if fmtString(v["side"]) != "response" {
					continue
				}
				kw := fmtString(v["keyword"])
				if kw != "response_shape" && kw != "truncated" && tc.opt != nil {
					// OpenAI target keeps raw response keywords; compatible must not.
					if tc.name != "openai_after_content_no_done" {
						t.Fatalf("raw response finding leaked: %v", v)
					}
				}
			}
			if spec := proxyResponseSpec(logBuf.String()); tc.name != "openai_after_content_no_done" && strings.HasPrefix(spec, "findings:") {
				t.Fatalf("compatible mid-stream noise counted as WARN: spec=%s vs=%v", spec, specViolations(call))
			}
		})
	}
}

func TestSessionsSpecCountersIgnoreResponseShape(t *testing.T) {
	// Compatible vendor extras + omitted optional keys: stored response_shape
	// (or none) must not move GET /sessions from clean into findings.
	body := chatJSON(`"timings":{"prompt_ms":1},"service_tier":"on_demand"`)
	p := startSpecProxy(t, "warn", "relaxed", chatJSONHandler(body), withCompatTarget())
	sid := newSession()
	status, _, raw, sess := postRaw(t, p, "/v1/chat/completions", sid, nil, chatJSONBody)
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, raw)
	}
	for _, v := range specViolations(lastCall(t, sess)) {
		if fmtString(v["side"]) == "response" && fmtString(v["keyword"]) != "response_shape" && fmtString(v["keyword"]) != "truncated" {
			t.Fatalf("raw response finding: %v", v)
		}
		if isWarnKeyword(fmtString(v["keyword"])) && fmtString(v["severity"]) != "info" {
			t.Fatalf("unexpected warn finding: %v", v)
		}
	}

	listedStatus, _, listed := getJSON(t, p.controlURL+"/sessions")
	if listedStatus != http.StatusOK {
		t.Fatalf("GET /sessions=%d", listedStatus)
	}
	spec := sessionSpecFromList(t, listed, sid)
	if asIntValue(spec["findings"]) != 0 || asIntValue(spec["clean"]) != 1 {
		t.Fatalf("spec=%v want clean=1 findings=0", spec)
	}
	if asIntValue(spec["request_only"]) != 0 || asIntValue(spec["not_checked"]) != 0 {
		t.Fatalf("spec=%v want request_only=0 not_checked=0", spec)
	}

	// A real request finding still increments findings even if response_shape is present.
	badReq := []byte(`{"model":"gpt-fixture","messages":[{"role":"user","content":"hi"}],"max_tokens":"nope"}`)
	sid2 := newSession()
	_, _, _, sess2 := postRaw(t, p, "/v1/chat/completions", sid2, nil, badReq)
	if len(specViolations(lastCall(t, sess2))) == 0 {
		t.Fatal("expected a request finding")
	}
	_, _, listed = getJSON(t, p.controlURL+"/sessions")
	spec2 := sessionSpecFromList(t, listed, sid2)
	if asIntValue(spec2["findings"]) != 1 || asIntValue(spec2["clean"]) != 0 {
		t.Fatalf("request finding spec=%v want findings=1", spec2)
	}
}

func sessionSpecFromList(t *testing.T, listed map[string]any, sid string) map[string]any {
	t.Helper()
	for _, item := range asAnySlice(listed["sessions"]) {
		s, _ := item.(map[string]any)
		if fmtString(s["id"]) != sid {
			continue
		}
		spec, _ := s["spec"].(map[string]any)
		if spec == nil {
			t.Fatalf("session %s missing spec: %v", sid, s)
		}
		return spec
	}
	t.Fatalf("session %s not listed: %v", sid, listed)
	return nil
}

func isWarnKeyword(keyword string) bool {
	switch keyword {
	case "response_shape", "truncated":
		return false
	default:
		return keyword != ""
	}
}
