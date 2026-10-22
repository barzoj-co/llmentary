// SPDX-License-Identifier: AGPL-3.0-only

package proxy_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/barzoj-co/llmentary/internal/proxy"
)

func chatNoLogprobs() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"chatcmpl-fake","object":"chat.completion","created":1,"model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":"Hello world"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`))
	}
}

func chatLogprobsNull() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"chatcmpl-fake","object":"chat.completion","created":1,"model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":"Hello world"},"finish_reason":"stop","logprobs":null}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`))
	}
}

func chatLogprobsWrongType() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"chatcmpl-fake","object":"chat.completion","created":1,"model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":"Hello world"},"finish_reason":"stop","logprobs":"nope"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`))
	}
}

func chatMissingMessage() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"chatcmpl-fake","object":"chat.completion","created":1,"model":"gpt-4o-mini","choices":[{"index":0,"finish_reason":"stop","logprobs":null}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`))
	}
}

func chatStreamNoLogprobs() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"id\":\"x\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"},\"finish_reason\":null}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}
}

func responsesMissingRelaxed() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"r","object":"response","created_at":1,"model":"gpt-fixture","status":"completed","output":[{"id":"m","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok","annotations":[],"logprobs":[]}]}],"parallel_tool_calls":true,"tool_choice":"auto","tools":[],"access_programs":null,"instructions":null,"metadata":{},"temperature":1,"top_p":1}`))
	}
}

func withOpenAITarget() func(*proxy.Config) {
	return func(cfg *proxy.Config) {
		if cfg.SpecTargets == nil {
			cfg.SpecTargets = map[string]string{}
		}
		cfg.SpecTargets["default"] = proxy.SpecTargetOpenAI
	}
}

func specViolations(call map[string]any) []map[string]any {
	var out []map[string]any
	for _, v := range asAnySlice(normalized(call)["spec_violations"]) {
		m, _ := v.(map[string]any)
		if m != nil {
			out = append(out, m)
		}
	}
	return out
}

func hasResponseShape(vs []map[string]any, pointer, needle string) bool {
	for _, v := range vs {
		if fmtString(v["keyword"]) != "response_shape" {
			continue
		}
		if pointer != "" && fmtString(v["pointer"]) != pointer {
			continue
		}
		if needle == "" || strings.Contains(fmtString(v["message"]), needle) {
			return true
		}
	}
	return false
}

func hasRequired(vs []map[string]any, pointer, prop string) bool {
	for _, v := range vs {
		if fmtString(v["keyword"]) != "required" {
			continue
		}
		if pointer != "" && fmtString(v["pointer"]) != pointer {
			continue
		}
		if strings.Contains(fmtString(v["message"]), "'"+prop+"'") {
			return true
		}
	}
	return false
}

func TestCompatibleOmitsLogprobsIsClean(t *testing.T) {
	logBuf := &syncLogBuf{}
	p := startSpecProxy(t, "warn", "relaxed", chatNoLogprobs(), withCompatTarget(), withTestLogger(logBuf))
	sid := newSession()
	status, _, raw, sess := postRaw(t, p, "/v1/chat/completions", sid, nil, chatJSONBody)
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, raw)
	}
	call := lastCall(t, sess)
	if hasRequired(specViolations(call), "/choices/0", "logprobs") {
		t.Fatalf("compatible still flagged logprobs: %v", normalized(call)["spec_violations"])
	}
	for _, line := range strings.Split(logBuf.String(), "\n") {
		if strings.Contains(line, "logprobs") && strings.Contains(line, "WARN") {
			t.Fatalf("WARN for relaxed key: %s", line)
		}
	}
}

func TestOpenAIOmitsLogprobsIsViolation(t *testing.T) {
	p := startSpecProxy(t, "warn", "relaxed", chatNoLogprobs(), withOpenAITarget())
	sid := newSession()
	_, _, _, sess := postRaw(t, p, "/v1/chat/completions", sid, nil, chatJSONBody)
	if !hasRequired(specViolations(lastCall(t, sess)), "/choices/0", "logprobs") {
		t.Fatalf("openai target dropped the required check: %v", normalized(lastCall(t, sess))["spec_violations"])
	}
}

func TestLogprobsNullCleanOnBothTargets(t *testing.T) {
	for _, opt := range []func(*proxy.Config){withCompatTarget(), withOpenAITarget()} {
		p := startSpecProxy(t, "warn", "relaxed", chatLogprobsNull(), opt)
		sid := newSession()
		_, _, _, sess := postRaw(t, p, "/v1/chat/completions", sid, nil, chatJSONBody)
		if hasRequired(specViolations(lastCall(t, sess)), "/choices/0", "logprobs") {
			t.Fatalf("null logprobs flagged: %v", normalized(lastCall(t, sess))["spec_violations"])
		}
	}
}

func TestLogprobsWrongTypeStillViolation(t *testing.T) {
	for name, opt := range map[string]func(*proxy.Config){"compatible": withCompatTarget(), "openai": withOpenAITarget()} {
		t.Run(name, func(t *testing.T) {
			p := startSpecProxy(t, "warn", "relaxed", chatLogprobsWrongType(), opt)
			sid := newSession()
			_, _, _, sess := postRaw(t, p, "/v1/chat/completions", sid, nil, chatJSONBody)
			found := false
			for _, v := range specViolations(lastCall(t, sess)) {
				if !strings.Contains(fmtString(v["pointer"]), "logprobs") {
					continue
				}
				kw := fmtString(v["keyword"])
				if kw == "type" || kw == "response_shape" {
					found = true
				}
			}
			if !found {
				t.Fatalf("type error dropped: %v", normalized(lastCall(t, sess))["spec_violations"])
			}
		})
	}
}

func TestMissingMessageStillViolation(t *testing.T) {
	p := startSpecProxy(t, "warn", "relaxed", chatMissingMessage(), withCompatTarget())
	sid := newSession()
	_, _, _, sess := postRaw(t, p, "/v1/chat/completions", sid, nil, chatJSONBody)
	vs := specViolations(lastCall(t, sess))
	if !hasRequired(vs, "/choices/0", "message") && !hasResponseShape(vs, "/choices/0", "message") {
		t.Fatalf("missing message rematched: %v", normalized(lastCall(t, sess))["spec_violations"])
	}
}

func TestStreamChunkWithoutLogprobsClean(t *testing.T) {
	p := startSpecProxy(t, "warn", "relaxed", chatStreamNoLogprobs(), withCompatTarget())
	sid := newSession()
	_, _, _, sess := postRaw(t, p, "/v1/chat/completions", sid, nil, chatStreamBody)
	if hasRequired(specViolations(lastCall(t, sess)), "", "logprobs") {
		t.Fatalf("stream logprobs: %v", normalized(lastCall(t, sess))["spec_violations"])
	}
}

func TestResponsesMissingRelaxedKeysCompatible(t *testing.T) {
	p := startSpecProxy(t, "warn", "relaxed", responsesMissingRelaxed(), withCompatTarget())
	sid := newSession()
	_, _, _, sess := postRaw(t, p, "/v1/responses", sid, nil, responsesJSONBody)
	vs := specViolations(lastCall(t, sess))
	if hasRequired(vs, "/", "error") || hasRequired(vs, "/", "incomplete_details") {
		t.Fatalf("relaxed Responses keys stayed violations: %v", vs)
	}
	keys := map[string]bool{}
	for _, raw := range asAnySlice(normalized(lastCall(t, sess))["ignored_fields"]) {
		f, _ := raw.(map[string]any)
		if fmtString(f["kind"]) == "compatible_optional" {
			keys[fmtString(f["key"])] = true
		}
	}
	if !keys["error"] || !keys["incomplete_details"] {
		t.Fatalf("expected one ignored_fields entry per relaxed key: %v", keys)
	}
}

func TestCompatibleOptionalKeysPinnedInDocs(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "providers.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, key := range proxy.CompatibleOptionalResponseRequired() {
		if !strings.Contains(text, key) {
			t.Errorf("docs/providers.md missing %q", key)
		}
	}
}
