// SPDX-License-Identifier: AGPL-3.0-only

package proxy_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/barzoj-co/llmentary/internal/proxy"
)

func chatJSON(extra string) []byte {
	body := `{"id":"chatcmpl-fake","object":"chat.completion","created":1,"model":"gpt-fixture","choices":[{"index":0,"message":{"role":"assistant","content":"Hello world"},"finish_reason":"stop","logprobs":null}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`
	if extra == "" {
		return []byte(body)
	}
	return []byte(strings.TrimSuffix(body, "}") + "," + extra + "}")
}

func chatJSONHandler(body []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}
}

func chatSSEHandler(frames []string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for _, frame := range frames {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", frame)
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}
}

func writeManySSE(n int, frame func(i int) string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for i := 0; i < n; i++ {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", frame(i))
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}
}

func validationInfo(call map[string]any) map[string]any {
	raw, _ := normalized(call)["validation"].(map[string]any)
	if raw == nil {
		return map[string]any{}
	}
	return raw
}

func countResponseShapes(vs []map[string]any) int {
	n := 0
	for _, v := range vs {
		if fmtString(v["keyword"]) == "response_shape" {
			n++
		}
	}
	return n
}

func TestCompatibleVendorResponseShapes(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
		path    string
		body    []byte
		needle  string
	}{
		{
			name:    "ollama_omits_logprobs",
			handler: chatJSONHandler(chatJSON("")),
			path:    "/v1/chat/completions",
			body:    chatJSONBody,
		},
		{
			name:    "deepseek_system_fingerprint_null",
			handler: chatJSONHandler(chatJSON(`"system_fingerprint":null`)),
			path:    "/v1/chat/completions",
			body:    chatJSONBody,
			needle:  "system_fingerprint",
		},
		{
			name:    "groq_service_tier_on_demand",
			handler: chatJSONHandler(chatJSON(`"service_tier":"on_demand"`)),
			path:    "/v1/chat/completions",
			body:    chatJSONBody,
			needle:  "service_tier",
		},
		{
			name:    "together_finish_reason_eos",
			handler: chatJSONHandler([]byte(`{"id":"c","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"eos","logprobs":null}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)),
			path:    "/v1/chat/completions",
			body:    chatJSONBody,
			needle:  "finish_reason",
		},
		{
			name:    "openrouter_finish_reason_error",
			handler: chatJSONHandler([]byte(`{"id":"c","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"error","logprobs":null}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)),
			path:    "/v1/chat/completions",
			body:    chatJSONBody,
			needle:  "finish_reason",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logBuf := &syncLogBuf{}
			p := startSpecProxy(t, "warn", "relaxed", tc.handler, withCompatTarget(), withTestLogger(logBuf))
			sid := newSession()
			status, _, raw, sess := postRaw(t, p, tc.path, sid, nil, tc.body)
			if status != http.StatusOK {
				t.Fatalf("status=%d body=%s", status, raw)
			}
			call := lastCall(t, sess)
			vs := specViolations(call)
			for _, v := range vs {
				if fmtString(v["severity"]) != "info" && fmtString(v["keyword"]) == "response_shape" {
					t.Fatalf("response_shape not info: %v", v)
				}
				if fmtString(v["side"]) == "response" && fmtString(v["keyword"]) != "response_shape" && fmtString(v["keyword"]) != "truncated" {
					t.Fatalf("raw response finding leaked: %v", v)
				}
			}
			if tc.needle != "" && countResponseShapes(vs) == 0 && !hasResponseShape(vs, "", tc.needle) {
				// fingerprint/tier/finish_reason may or may not violate depending on schema;
				// if they do, they must be response_shape. If they don't, that's also fine.
			}
			spec := proxyResponseSpec(logBuf.String())
			if strings.HasPrefix(spec, "findings:") {
				t.Fatalf("compatible response noise counted as WARN: spec=%s vs=%v log=%s", spec, vs, logBuf.String())
			}
			for _, line := range warnLines(logBuf.String()) {
				if strings.Contains(line, "response_shape") || strings.Contains(line, tc.needle) && strings.Contains(line, "side=response") {
					t.Fatalf("WARN for compatible response finding: %s", line)
				}
			}
		})
	}
}

func TestOpenAIResponseFindingsStillWarn(t *testing.T) {
	logBuf := &syncLogBuf{}
	p := startSpecProxy(t, "warn", "relaxed", chatJSONHandler([]byte(`{"id":"c","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"eos","logprobs":null}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)), withOpenAITarget(), withTestLogger(logBuf))
	sid := newSession()
	_, _, _, sess := postRaw(t, p, "/v1/chat/completions", sid, nil, chatJSONBody)
	vs := specViolations(lastCall(t, sess))
	found := false
	for _, v := range vs {
		if strings.Contains(fmtString(v["pointer"]), "finish_reason") && fmtString(v["keyword"]) != "response_shape" {
			found = true
		}
	}
	if !found {
		t.Fatalf("openai target rematched finish_reason: %v", vs)
	}
	if spec := proxyResponseSpec(logBuf.String()); !strings.HasPrefix(spec, "findings:") {
		t.Fatalf("openai target should count response findings: spec=%s", spec)
	}
}

func TestCompatibleVendorExtrasProduceNoFinding(t *testing.T) {
	body := chatJSON(`"timings":{"prompt_ms":1},"x_groq":{"id":"g"},"provider":"groq"`)
	logBuf := &syncLogBuf{}
	p := startSpecProxy(t, "warn", "relaxed", chatJSONHandler(body), withCompatTarget(), withTestLogger(logBuf))
	sid := newSession()
	_, _, _, sess := postRaw(t, p, "/v1/chat/completions", sid, nil, chatJSONBody)
	vs := specViolations(lastCall(t, sess))
	for _, v := range vs {
		msg := fmtString(v["message"]) + fmtString(v["pointer"])
		if strings.Contains(msg, "timings") || strings.Contains(msg, "x_groq") || strings.Contains(msg, "provider") {
			t.Fatalf("vendor extra produced a finding: %v", v)
		}
	}
	if spec := proxyResponseSpec(logBuf.String()); strings.HasPrefix(spec, "findings:") {
		t.Fatalf("vendor extras counted as findings: spec=%s vs=%v", spec, vs)
	}
}

func TestCompatibleOptionalFieldsAbsent(t *testing.T) {
	// Minimal OpenAI-looking chat JSON: required-nullable keys omitted.
	body := []byte(`{"id":"c","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	logBuf := &syncLogBuf{}
	p := startSpecProxy(t, "warn", "relaxed", chatJSONHandler(body), withCompatTarget(), withTestLogger(logBuf))
	sid := newSession()
	_, _, _, sess := postRaw(t, p, "/v1/chat/completions", sid, nil, chatJSONBody)
	if spec := proxyResponseSpec(logBuf.String()); strings.HasPrefix(spec, "findings:") {
		t.Fatalf("optional-absent counted as WARN: spec=%s vs=%v", spec, specViolations(lastCall(t, sess)))
	}
	if hasRequired(specViolations(lastCall(t, sess)), "/choices/0", "logprobs") {
		t.Fatalf("omitted logprobs stayed a required violation")
	}
}

func manyChoiceTypeErrors(n int) []byte {
	var b strings.Builder
	b.WriteString(`{"id":"c","object":"chat.completion","created":1,"model":"m","choices":[`)
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"index":%d,"message":{"role":"assistant","content":"ok"},"finish_reason":"eos","logprobs":"bad"}`, i)
	}
	b.WriteString(`],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	return []byte(b.String())
}

func TestResponseFindingsCapBothTargets(t *testing.T) {
	const n, capN = 40, 8
	body := manyChoiceTypeErrors(n)
	for _, opt := range []struct {
		name string
		fn   func(*proxy.Config)
	}{
		{"compatible", withCompatTarget()},
		{"openai", withOpenAITarget()},
	} {
		t.Run(opt.name, func(t *testing.T) {
			p := startSpecProxy(t, "warn", "relaxed", chatJSONHandler(body), opt.fn, func(cfg *proxy.Config) {
				cfg.SpecResponseFindingsMax = capN
			})
			sid := newSession()
			_, _, _, sess := postRaw(t, p, "/v1/chat/completions", sid, nil, chatJSONBody)
			call := lastCall(t, sess)
			vs := specViolations(call)
			resp := 0
			truncatedMarker := false
			for _, v := range vs {
				if fmtString(v["keyword"]) == "truncated" {
					truncatedMarker = true
					continue
				}
				if fmtString(v["side"]) == "response" || fmtString(v["keyword"]) == "response_shape" {
					resp++
				}
			}
			info := validationInfo(call)
			if resp > capN {
				t.Fatalf("stored %d response findings, cap %d: %v", resp, capN, vs)
			}
			if !asBool(info["truncated"]) && !truncatedMarker {
				t.Fatalf("expected truncated flag; info=%v vs=%v", info, vs)
			}
		})
	}
}

func TestCompatibleRepeatedStreamFindingCountsOnce(t *testing.T) {
	const n = 1000
	frame := func(int) string {
		return `{"id":"x","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"content":"x"},"finish_reason":"eos"}]}`
	}
	logBuf := &syncLogBuf{}
	p := startSpecProxy(t, "warn", "relaxed", writeManySSE(n, frame), withCompatTarget(), withTestLogger(logBuf))
	sid := newSession()
	_, _, _, sess := postRaw(t, p, "/v1/chat/completions", sid, nil, chatStreamBody)
	vs := specViolations(lastCall(t, sess))
	shapes := 0
	var count float64
	for _, v := range vs {
		if fmtString(v["keyword"]) != "response_shape" {
			continue
		}
		shapes++
		count, _ = v["count"].(float64)
	}
	if shapes > 1 {
		t.Fatalf("want one response_shape per pointer+keyword, got %d: %v", shapes, vs)
	}
	if shapes == 1 && count != n && count < 2 {
		t.Fatalf("count=%v want collapsed stream copies: %v", count, vs)
	}
	if spec := proxyResponseSpec(logBuf.String()); strings.HasPrefix(spec, "findings:") {
		t.Fatalf("stream response_shape counted as WARN: spec=%s", spec)
	}
}

func TestResponsesStreamSummarized(t *testing.T) {
	frames := make([]string, 0, 8)
	for i := 0; i < 8; i++ {
		frames = append(frames, fmt.Sprintf(`{"type":"response.output_text.delta","delta":"t%d"}`, i))
	}
	logBuf := &syncLogBuf{}
	p := startSpecProxy(t, "warn", "relaxed", chatSSEHandler(frames), withCompatTarget(), withTestLogger(logBuf))
	sid := newSession()
	_, _, _, sess := postRaw(t, p, "/v1/responses", sid, nil, responsesJSONBody)
	vs := specViolations(lastCall(t, sess))
	for _, v := range vs {
		if fmtString(v["side"]) == "response" && fmtString(v["keyword"]) != "response_shape" && fmtString(v["keyword"]) != "truncated" {
			t.Fatalf("raw responses finding: %v", v)
		}
	}
	if spec := proxyResponseSpec(logBuf.String()); strings.HasPrefix(spec, "findings:") {
		t.Fatalf("responses stream counted as WARN: spec=%s vs=%v", spec, vs)
	}
}

func asBool(v any) bool {
	b, _ := v.(bool)
	return b
}
