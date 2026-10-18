// SPDX-License-Identifier: AGPL-3.0-only

package proxy_test

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/barzoj-co/llm-test-lab/internal/lab"
	"github.com/barzoj-co/llm-test-lab/internal/specvalidate"
)

func TestA2CLIOverridesEnv(t *testing.T) {
	_, control, _ := startBinary(t, []string{
		"--spec-validation=enforce",
		"--spec-profile=strict",
		"--upstream", "https://example.com",
	}, []string{
		"LLMTESTLAB_SPEC_VALIDATION=off",
		"LLMTESTLAB_SPEC_PROFILE=relaxed",
	})
	cfg := getConfig(t, control)
	if cfg["spec_validation"] != "enforce" || cfg["spec_profile"] != "strict" {
		t.Fatalf("CLI should override env: config=%v", cfg)
	}
}

func TestA3ProfilesCompileAndCorrupted(t *testing.T) {
	t.Run("warn_relaxed_healthy", func(t *testing.T) {
		_, control, _ := startBinary(t, []string{
			"--spec-validation=warn",
			"--spec-profile=relaxed",
			"--upstream", "https://example.com",
		}, nil)
		res, err := http.Get(control + "/healthz")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatalf("status=%d", res.StatusCode)
		}
	})
	t.Run("corrupted_schema_fails_closed", func(t *testing.T) {
		cmd := exec.Command("go", "run", "-tags", "corrupted_schema", "./cmd/llmtestlab", "--upstream", "https://example.com")
		cmd.Dir = filepath.Join("..", "..")
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("expected non-zero exit, output=%s", out)
		}
		low := strings.ToLower(string(out))
		if !strings.Contains(low, "compile") && !strings.Contains(low, "spec") && !strings.Contains(low, "schema") {
			t.Fatalf("output should mention compile/spec/schema: %s", out)
		}
	})
	t.Run("off_does_not_compile", func(t *testing.T) {
		before := specvalidate.CompileCount.Load()
		_ = startSpecProxy(t, "off", "relaxed", chatOK())
		after := specvalidate.CompileCount.Load()
		if after != before {
			t.Fatalf("off incremented CompileCount %d -> %d", before, after)
		}
	})
}

func TestA6OverlayStaleProofRunsScript(t *testing.T) {
	root := filepath.Join("..", "..")
	script := filepath.Join(root, "scripts", "prove-a6.sh")
	if _, err := os.Stat(script); err != nil {
		t.Skip("scripts/prove-a6.sh not present")
	}
	cmd := exec.Command("bash", script)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("prove-a6.sh: %v\n%s", err, out)
	}
}

func TestD3ResponsesToolsEnumDescriptions(t *testing.T) {
	for _, strict := range []any{true, false, nil} {
		t.Run(fmt.Sprintf("strict=%v", strict), func(t *testing.T) {
			tool := map[string]any{
				"type": "function",
				"name": "shirt",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"color": map[string]any{
							"type":             "string",
							"enum":             []any{"red", "blue"},
							"enumDescriptions": map[string]any{"red": "warm", "blue": "cool"},
						},
					},
				},
			}
			if strict != nil {
				tool["strict"] = strict
			}
			body, _ := json.Marshal(map[string]any{"model": "m", "input": "hi", "tools": []any{tool}})
			p := startSpecProxy(t, "warn", "relaxed", responsesOK())
			sid := newSession()
			_, _, _, session := postRaw(t, p, "/v1/responses", sid, nil, body)
			assertIgnoredKind(t, normalized(lastCall(t, session)), "unknown_schema_keyword")
			if !hasIgnoredKey(normalized(lastCall(t, session)), "enumDescriptions") {
				t.Fatalf("missing enumDescriptions: %v", normalized(lastCall(t, session))["ignored_fields"])
			}
		})
	}
}

func TestD4JSONSchemaContexts(t *testing.T) {
	t.Run("chat_response_format", func(t *testing.T) {
		body := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"response_format":{"type":"json_schema","json_schema":{"name":"out","strict":true,"schema":{"type":"object","properties":{"x":{"type":"string","enum":["a"],"enumDescriptions":{"a":"aye"}}},"required":["x"],"additionalProperties":false}}}}`)
		p := startSpecProxy(t, "warn", "relaxed", chatOK())
		sid := newSession()
		_, _, _, session := postRaw(t, p, "/v1/chat/completions", sid, nil, body)
		n := normalized(lastCall(t, session))
		if !hasIgnoredKey(n, "enumDescriptions") {
			t.Fatalf("chat json_schema: %v", n["ignored_fields"])
		}
		if !hasIgnoredContext(n, "chat.response_format.json_schema") {
			t.Fatalf("context: %v", n["ignored_fields"])
		}
	})
	t.Run("responses_text_format", func(t *testing.T) {
		body := []byte(`{"model":"m","input":"hi","text":{"format":{"type":"json_schema","name":"out","strict":true,"schema":{"type":"object","properties":{"x":{"type":"string","enum":["a"],"enumDescriptions":{"a":"aye"}}},"required":["x"],"additionalProperties":false}}}}`)
		p := startSpecProxy(t, "warn", "relaxed", responsesOK())
		sid := newSession()
		_, _, _, session := postRaw(t, p, "/v1/responses", sid, nil, body)
		n := normalized(lastCall(t, session))
		if !hasIgnoredKey(n, "enumDescriptions") {
			t.Fatalf("responses text.format: %v", n["ignored_fields"])
		}
		if !hasIgnoredContext(n, "responses.text.format") {
			t.Fatalf("context: %v", n["ignored_fields"])
		}
	})
}

func TestD6OneOfAndMetaKeywords(t *testing.T) {
	body := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"x","parameters":{"type":"object","$schema":"https://json-schema.org/draft/2020-12/schema","$id":"https://example.com/s","title":"MySchema","oneOf":[{"type":"object"}],"properties":{"a":{"type":"string"}}}}}]}`)
	p := startSpecProxy(t, "warn", "relaxed", chatOK())
	sid := newSession()
	_, _, _, session := postRaw(t, p, "/v1/chat/completions", sid, nil, body)
	n := normalized(lastCall(t, session))
	for _, key := range []string{"oneOf", "$schema", "$id", "title"} {
		if !hasIgnoredKey(n, key) {
			t.Fatalf("missing %s in %v", key, n["ignored_fields"])
		}
	}
}

func TestD9RemainingContexts(t *testing.T) {
	cases := []struct {
		name, path, context string
		body                []byte
		up                  http.HandlerFunc
	}{
		{"functions", "/v1/chat/completions", "chat.functions.parameters", []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"functions":[{"name":"shirt","parameters":{"type":"object","properties":{"color":{"type":"string","enum":["red"],"enumDescriptions":{"red":"warm"}}}}}]}`), chatOK()},
		{"responses_tools", "/v1/responses", "responses.tools.parameters", []byte(`{"model":"m","input":"hi","tools":[{"type":"function","name":"shirt","parameters":{"type":"object","properties":{"color":{"type":"string","enum":["red"],"enumDescriptions":{"red":"warm"}}}}}]}`), responsesOK()},
		{"json_schema", "/v1/chat/completions", "chat.response_format.json_schema", []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"response_format":{"type":"json_schema","json_schema":{"name":"o","schema":{"type":"object","properties":{"c":{"enum":["r"],"enumDescriptions":{"r":"w"}}}}}}}`), chatOK()},
		{"text_format", "/v1/responses", "responses.text.format", []byte(`{"model":"m","input":"hi","text":{"format":{"type":"json_schema","name":"o","schema":{"type":"object","properties":{"c":{"enum":["r"],"enumDescriptions":{"r":"w"}}}}}}}`), responsesOK()},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := startSpecProxy(t, "warn", "relaxed", c.up)
			sid := newSession()
			_, _, _, session := postRaw(t, p, c.path, sid, nil, c.body)
			n := normalized(lastCall(t, session))
			if !hasIgnoredKey(n, "enumDescriptions") {
				t.Fatalf("missing enumDescriptions: %v", n["ignored_fields"])
			}
			if !hasIgnoredContext(n, c.context) {
				t.Fatalf("want context %s: %v", c.context, n["ignored_fields"])
			}
		})
	}
}

func TestE4AnyOfProxy(t *testing.T) {
	t.Run("branches_linted", func(t *testing.T) {
		body := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"x","parameters":{"anyOf":[{"type":"string"},{"type":"object","x-foo":1,"enumDescriptions":{"a":"b"}}]}}}]}`)
		p := startSpecProxy(t, "warn", "relaxed", chatOK())
		sid := newSession()
		_, _, _, session := postRaw(t, p, "/v1/chat/completions", sid, nil, body)
		n := normalized(lastCall(t, session))
		if !hasIgnoredKey(n, "x-foo") || !hasIgnoredKey(n, "enumDescriptions") {
			t.Fatalf("anyOf branches: %v", n["ignored_fields"])
		}
	})
	t.Run("root_anyOf_strict", func(t *testing.T) {
		body := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"x","strict":true,"parameters":{"anyOf":[{"type":"object","properties":{"a":{"type":"string"}},"additionalProperties":false,"required":["a"]}]}}}]}`)
		p := startSpecProxy(t, "warn", "relaxed", chatOK())
		sid := newSession()
		_, _, _, session := postRaw(t, p, "/v1/chat/completions", sid, nil, body)
		if !hasViolationSource(normalized(lastCall(t, session)), "strict_mode", "anyOf") {
			t.Fatalf("expected strict_mode anyOf: %v", normalized(lastCall(t, session))["spec_violations"])
		}
	})
	t.Run("null_pattern", func(t *testing.T) {
		body := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"x","parameters":{"type":"object","properties":{"a":{"anyOf":[{"type":"string"},{"type":"null"}]}}}}}]}`)
		p := startSpecProxy(t, "warn", "relaxed", chatOK())
		sid := newSession()
		_, _, _, session := postRaw(t, p, "/v1/chat/completions", sid, nil, body)
		n := normalized(lastCall(t, session))
		if len(asAnySlice(n["ignored_fields"])) != 0 {
			t.Fatalf("null pattern findings=%v", n["ignored_fields"])
		}
		if requestViolations(n) != 0 {
			t.Fatalf("null pattern violations=%v", n["spec_violations"])
		}
	})
}

func TestE5AdditionalPropertiesProxy(t *testing.T) {
	t.Run("schema_walked", func(t *testing.T) {
		body := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"x","parameters":{"type":"object","additionalProperties":{"type":"string","enumDescriptions":{"a":"b"}}}}}]}`)
		p := startSpecProxy(t, "warn", "relaxed", chatOK())
		sid := newSession()
		_, _, _, session := postRaw(t, p, "/v1/chat/completions", sid, nil, body)
		if !hasIgnoredKey(normalized(lastCall(t, session)), "enumDescriptions") {
			t.Fatal("additionalProperties schema not walked")
		}
	})
	t.Run("missing_under_strict_true", func(t *testing.T) {
		body := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"x","strict":true,"parameters":{"type":"object","properties":{"a":{"type":"string"}},"required":["a"]}}}]}`)
		p := startSpecProxy(t, "warn", "relaxed", chatOK())
		sid := newSession()
		_, _, _, session := postRaw(t, p, "/v1/chat/completions", sid, nil, body)
		if !hasViolationSource(normalized(lastCall(t, session)), "strict_mode", "additionalProperties") {
			t.Fatalf("%v", normalized(lastCall(t, session))["spec_violations"])
		}
	})
	t.Run("omitted_strict_responses", func(t *testing.T) {
		body := []byte(`{"model":"m","input":"hi","tools":[{"type":"function","name":"x","parameters":{"type":"object","properties":{"a":{"type":"string"}}}}]}`)
		p := startSpecProxy(t, "warn", "relaxed", responsesOK())
		sid := newSession()
		_, _, _, session := postRaw(t, p, "/v1/responses", sid, nil, body)
		assertIgnoredKind(t, normalized(lastCall(t, session)), "strict_fallback_predicted")
	})
	t.Run("extra_props_inside_parameters_not_spec_violations", func(t *testing.T) {
		body := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"x","parameters":{"type":"object","properties":{"a":{"type":"string"}},"customVendorKey":true,"x-extra":1}}}}]}`)
		p := startSpecProxy(t, "warn", "strict", chatOK())
		sid := newSession()
		_, _, _, session := postRaw(t, p, "/v1/chat/completions", sid, nil, body)
		n := normalized(lastCall(t, session))
		for _, v := range asAnySlice(n["spec_violations"]) {
			m := v.(map[string]any)
			if m["side"] == "response" {
				continue
			}
			ptr, _ := m["pointer"].(string)
			if strings.Contains(ptr, "/parameters/") || strings.Contains(ptr, "/parameters") {
				if m["keyword"] == "unevaluatedProperties" || m["keyword"] == "additionalProperties" {
					t.Fatalf("parameters extra props should not be spec violations: %v", m)
				}
			}
		}
	})
}

func TestE6NestingAndLimitsProxy(t *testing.T) {
	p := startSpecProxy(t, "warn", "relaxed", chatOK())
	t.Run("nesting_10_ok", func(t *testing.T) {
		sid := newSession()
		_, _, _, session := postRaw(t, p, "/v1/chat/completions", sid, nil, strictToolBody(measuredNesting(10)))
		if hasViolationKeyword(normalized(lastCall(t, session)), "maxNesting") {
			t.Fatalf("depth 10: %v", normalized(lastCall(t, session))["spec_violations"])
		}
	})
	t.Run("nesting_11_flagged", func(t *testing.T) {
		sid := newSession()
		_, _, _, session := postRaw(t, p, "/v1/chat/completions", sid, nil, strictToolBody(measuredNesting(11)))
		if !hasViolationSource(normalized(lastCall(t, session)), "strict_mode", "maxNesting") {
			t.Fatalf("depth 11: %v", normalized(lastCall(t, session))["spec_violations"])
		}
	})
	t.Run("properties_5000_ok", func(t *testing.T) {
		sid := newSession()
		_, _, _, session := postRaw(t, p, "/v1/chat/completions", sid, nil, strictToolBody(nPropSchema(5000)))
		if hasViolationKeyword(normalized(lastCall(t, session)), "maxProperties") {
			t.Fatalf("5000: %v", normalized(lastCall(t, session))["spec_violations"])
		}
	})
	t.Run("properties_5001_flagged", func(t *testing.T) {
		sid := newSession()
		_, _, _, session := postRaw(t, p, "/v1/chat/completions", sid, nil, strictToolBody(nPropSchema(5001)))
		if !hasViolationKeyword(normalized(lastCall(t, session)), "maxProperties") {
			t.Fatalf("5001: %v", normalized(lastCall(t, session))["spec_violations"])
		}
	})
	t.Run("enum_1000_ok", func(t *testing.T) {
		sid := newSession()
		_, _, _, session := postRaw(t, p, "/v1/chat/completions", sid, nil, strictToolBody(enumCountSchema(1000, 1)))
		if hasViolationKeyword(normalized(lastCall(t, session)), "maxEnumValues") {
			t.Fatalf("1000: %v", normalized(lastCall(t, session))["spec_violations"])
		}
	})
	t.Run("enum_1001_flagged", func(t *testing.T) {
		sid := newSession()
		_, _, _, session := postRaw(t, p, "/v1/chat/completions", sid, nil, strictToolBody(enumCountSchema(1001, 1)))
		if !hasViolationKeyword(normalized(lastCall(t, session)), "maxEnumValues") {
			t.Fatalf("1001: %v", normalized(lastCall(t, session))["spec_violations"])
		}
	})
	t.Run("name_chars_and_long_enum", func(t *testing.T) {
		sid := newSession()
		_, _, _, session := postRaw(t, p, "/v1/chat/completions", sid, nil, strictToolBody(enumCountSchema(251, 60)))
		if !hasViolationKeyword(normalized(lastCall(t, session)), "maxNameChars") {
			t.Fatalf("251-value enum >15000 chars: %v", normalized(lastCall(t, session))["spec_violations"])
		}
	})
}

func TestE11TruncatedFindings(t *testing.T) {
	schema := map[string]any{"type": "object"}
	for i := 0; i < 500; i++ {
		schema[fmt.Sprintf("x-%d", i)] = true
	}
	body, _ := json.Marshal(map[string]any{
		"model":    "m",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
		"tools":    []any{map[string]any{"type": "function", "function": map[string]any{"name": "x", "parameters": schema}}},
	})
	p := startSpecProxy(t, "warn", "relaxed", chatOK())
	sid := newSession()
	_, _, _, session := postRaw(t, p, "/v1/chat/completions", sid, nil, body)
	n := normalized(lastCall(t, session))
	ignored := asAnySlice(n["ignored_fields"])
	if len(ignored) < specvalidate.MaxFindings {
		t.Fatalf("findings=%d", len(ignored))
	}
	last := ignored[len(ignored)-1].(map[string]any)
	if last["kind"] != "truncated" && last["key"] != "truncated" {
		t.Fatalf("missing truncated marker: %v", last)
	}
	val, _ := n["validation"].(map[string]any)
	if val["truncated"] != true {
		t.Fatalf("validation.truncated=%v", val["truncated"])
	}
}

func TestF1InvalidUTF8AndBOM(t *testing.T) {
	t.Run("warn_forwards_invalid_utf8", func(t *testing.T) {
		var body []byte
		body = append(body, []byte(`{"model":"m","messages":[{"role":"user","content":"`)...)
		body = append(body, 0xff, 0xfe)
		body = append(body, []byte(`"}]}`)...)
		p := startSpecProxy(t, "warn", "strict", chatOK())
		sid := newSession()
		_, _, _, session := postRaw(t, p, "/v1/chat/completions", sid, nil, body)
		if p.upstream.Count() != 1 {
			t.Fatal("not forwarded")
		}
		got := p.upstream.LastBody()
		if !bytes.Contains(got, []byte{0xff, 0xfe}) {
			t.Fatalf("0xff 0xfe replaced; body=%q", got)
		}
		if bytes.Contains(got, []byte("\ufffd")) {
			t.Fatal("U+FFFD substitution")
		}
		if !hasViolationKeyword(normalized(lastCall(t, session)), "invalidUTF8") {
			t.Fatalf("strict missing invalidUTF8: %v", normalized(lastCall(t, session))["spec_violations"])
		}
	})
	t.Run("bom_prefix", func(t *testing.T) {
		plain := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
		body := append([]byte{0xEF, 0xBB, 0xBF}, plain...)
		pStrict := startSpecProxy(t, "warn", "strict", chatOK())
		sid := newSession()
		_, _, _, session := postRaw(t, pStrict, "/v1/chat/completions", sid, nil, body)
		if !hasViolationKeyword(normalized(lastCall(t, session)), "utf8BOM") {
			t.Fatalf("strict BOM: %v", normalized(lastCall(t, session))["spec_violations"])
		}
		if !bytes.Equal(pStrict.upstream.LastBody(), body) {
			t.Fatal("BOM bytes mutated")
		}
		pRel := startSpecProxy(t, "warn", "relaxed", chatOK())
		sid2 := newSession()
		_, _, _, session2 := postRaw(t, pRel, "/v1/chat/completions", sid2, nil, body)
		if hasViolationKeyword(normalized(lastCall(t, session2)), "utf8BOM") {
			t.Fatalf("relaxed should skip utf8BOM: %v", normalized(lastCall(t, session2))["spec_violations"])
		}
	})
}

func TestF2BodyTooLarge(t *testing.T) {
	t.Run("over_limit_skipped_and_forwarded", func(t *testing.T) {
		p := startSpecProxy(t, "enforce", "strict", chatOK(), func(cfg *lab.Config) {
			cfg.SpecValidationMaxBytes = 1 << 20
		})
		body := sizedChatBody(20 << 20)
		sid := newSession()
		status, _, _, session := postRaw(t, p, "/v1/chat/completions", sid, nil, body)
		if status == 400 {
			t.Fatal("enforce must not 400 a skipped oversized body")
		}
		if p.upstream.Count() != 1 {
			t.Fatal("not forwarded")
		}
		n := normalized(lastCall(t, session))
		val, _ := n["validation"].(map[string]any)
		if fmt.Sprint(val["skipped_reason"]) != "body_too_large" {
			t.Fatalf("skipped_reason=%v", val["skipped_reason"])
		}
		if val["request_checked"] != false {
			t.Fatalf("request_checked=%v", val["request_checked"])
		}
	})
	t.Run("exact_at_limit_validated", func(t *testing.T) {
		const limit = 256
		p := startSpecProxy(t, "warn", "strict", chatOK(), func(cfg *lab.Config) {
			cfg.SpecValidationMaxBytes = limit
		})
		body := sizedChatBody(limit)
		if len(body) != limit {
			t.Fatalf("len=%d", len(body))
		}
		sid := newSession()
		_, _, _, session := postRaw(t, p, "/v1/chat/completions", sid, nil, body)
		val, _ := normalized(lastCall(t, session))["validation"].(map[string]any)
		if val["request_checked"] != true {
			t.Fatalf("exact-at-limit should be validated: %v", val)
		}
		if val["skipped_reason"] != nil {
			t.Fatalf("skipped_reason=%v", val["skipped_reason"])
		}
	})
}

func TestF3DeeplyNestedMetadata(t *testing.T) {
	deep := strings.Repeat("[", 100000) + strings.Repeat("]", 100000)
	body := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"metadata":{"x":` + deep + `}}`)
	p := startSpecProxy(t, "warn", "strict", chatOK())
	sid := newSession()
	_, _, _, session := postRaw(t, p, "/v1/chat/completions", sid, nil, body)
	n := normalized(lastCall(t, session))
	if !hasViolationKeyword(n, "maxDepth") {
		t.Fatalf("expected maxDepth: %v", n["spec_violations"])
	}
	sid2 := newSession()
	status, _, resp, session2 := postRaw(t, p, "/v1/chat/completions", sid2, nil, []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`))
	if status != 200 {
		t.Fatalf("follow-up status=%d body=%s", status, resp)
	}
	if requestViolations(normalized(lastCall(t, session2))) != 0 {
		t.Fatalf("follow-up dirty: %v", normalized(lastCall(t, session2))["spec_violations"])
	}
}

func TestF7ContentEncoding(t *testing.T) {
	plain := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
	t.Run("gzip_validated_and_forwarded", func(t *testing.T) {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		_, _ = zw.Write(plain)
		_ = zw.Close()
		gz := buf.Bytes()
		p := startSpecProxy(t, "warn", "relaxed", chatOK())
		sid := newSession()
		_, _, _, session := postRaw(t, p, "/v1/chat/completions", sid, map[string]string{
			"Content-Encoding": "gzip",
			"Content-Type":     "application/json",
		}, gz)
		if !bytes.Equal(p.upstream.LastBody(), gz) {
			t.Fatalf("upstream did not receive gzipped bytes")
		}
		val, _ := normalized(lastCall(t, session))["validation"].(map[string]any)
		if val["request_checked"] != true {
			t.Fatalf("gzip should be validated: %v", val)
		}
	})
	t.Run("unsupported_encoding", func(t *testing.T) {
		p := startSpecProxy(t, "warn", "relaxed", chatOK())
		sid := newSession()
		_, _, _, session := postRaw(t, p, "/v1/chat/completions", sid, map[string]string{
			"Content-Encoding": "br",
		}, plain)
		val, _ := normalized(lastCall(t, session))["validation"].(map[string]any)
		if fmt.Sprint(val["skipped_reason"]) != "unsupported_content_encoding" {
			t.Fatalf("skipped_reason=%v", val["skipped_reason"])
		}
	})
	t.Run("non_json_content_type", func(t *testing.T) {
		p := startSpecProxy(t, "warn", "relaxed", chatOK())
		sid := newSession()
		_, _, _, session := postRaw(t, p, "/v1/chat/completions", sid, map[string]string{
			"Content-Type": "text/plain",
		}, []byte("this is not json"))
		val, _ := normalized(lastCall(t, session))["validation"].(map[string]any)
		if fmt.Sprint(val["skipped_reason"]) != "non_json_content_type" {
			t.Fatalf("skipped_reason=%v", val["skipped_reason"])
		}
	})
}

func TestF8StreamUnknownRejectedAsJSON(t *testing.T) {
	body := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"stream":true,"foo":1}`)
	p := startSpecProxy(t, "enforce", "strict", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n"))
	})
	sid := newSession()
	status, hdr, resp, _ := postRaw(t, p, "/v1/chat/completions", sid, map[string]string{
		"Accept": "text/event-stream",
	}, body)
	if status != 400 {
		t.Fatalf("status=%d body=%s", status, resp)
	}
	ct := hdr.Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Fatalf("Content-Type=%q", ct)
	}
	if strings.Contains(ct, "event-stream") {
		t.Fatalf("must not be event-stream: %q", ct)
	}
	if hdr.Get("X-LLMTestLab-Rejected") == "" {
		t.Fatal("missing X-LLMTestLab-Rejected")
	}
	if p.upstream.Count() != 0 {
		t.Fatal("upstream called")
	}
}

func TestG1FunctionCallArgumentsDoneWithoutName(t *testing.T) {
	// Overlay #545 is not applied: the pinned spec still requires `name` on
	// response.function_call_arguments.done. That requirement is not live-verified.
	payload := `{"type":"response.function_call_arguments.done","item_id":"fc_1","output_index":0,"arguments":"{}","sequence_number":1}`
	stream := "event: response.function_call_arguments.done\ndata: " + payload + "\n\n"
	up := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, stream)
	}
	body := []byte(`{"model":"m","input":"hi"}`)
	p := startSpecProxy(t, "warn", "strict", up)
	sid := newSession()
	_, _, client, session := postRaw(t, p, "/v1/responses", sid, nil, body)
	if !bytes.Equal(client, []byte(stream)) {
		t.Fatalf("client bytes changed\n%s\n%s", client, stream)
	}
	found := false
	for _, v := range asAnySlice(normalized(lastCall(t, session))["spec_violations"]) {
		m := v.(map[string]any)
		if m["side"] == "response" && m["event_index"] != nil {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected response-side violation with event_index (pinned spec requires name; overlay #545 not applied): %v", normalized(lastCall(t, session))["spec_violations"])
	}
	p2 := startSpecProxy(t, "enforce", "strict", up)
	sid2 := newSession()
	status, _, _, _ := postRaw(t, p2, "/v1/responses", sid2, nil, body)
	if status == 400 {
		t.Fatal("enforce must still forward response-side violations")
	}
	if p2.upstream.Count() != 1 {
		t.Fatalf("upstream=%d", p2.upstream.Count())
	}
}

func TestG3ResponseViolationEnforceForwards(t *testing.T) {
	up := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":1,"choices":"bad"}`))
	}
	p := startSpecProxy(t, "enforce", "strict", up)
	sid := newSession()
	status, _, _, session := postRaw(t, p, "/v1/chat/completions", sid, nil, []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`))
	if status == 400 {
		t.Fatal("response-side violation must not 400 under enforce")
	}
	if p.upstream.Count() != 1 {
		t.Fatalf("upstream=%d", p.upstream.Count())
	}
	found := false
	for _, v := range asAnySlice(normalized(lastCall(t, session))["spec_violations"]) {
		if v.(map[string]any)["side"] == "response" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected response-side violation: %v", normalized(lastCall(t, session))["spec_violations"])
	}
}

func TestG4SSEInterChunkP95(t *testing.T) {
	up := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		for i := 0; i < 50; i++ {
			_, _ = fmt.Fprintf(w, "data: {\"id\":\"c\",\"choices\":[{\"delta\":{\"content\":\"%02d\"}}]}\n\n", i)
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	pWarn := startSpecProxy(t, "warn", "relaxed", up)
	pOff := startSpecProxy(t, "off", "relaxed", up)
	body := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"stream":true}`)
	warnP95 := streamP95(t, pWarn, body)
	offP95 := streamP95(t, pOff, body)
	delta := warnP95 - offP95
	if delta < 0 {
		delta = -delta
	}
	if delta > 5*time.Millisecond {
		t.Fatalf("p95 delta=%s warn=%s off=%s (want <=5ms)", delta, warnP95, offP95)
	}
}

func TestG5TrailingAndConcatenated(t *testing.T) {
	t.Run("responses_non_stream_trailing", func(t *testing.T) {
		up := func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"r","model":"gpt-fixture","status":"completed","output":[{"id":"m","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok","annotations":[],"logprobs":[]}]}]} trailing`))
		}
		p := startSpecProxy(t, "warn", "relaxed", up)
		sid := newSession()
		_, _, client, session := postRaw(t, p, "/v1/responses", sid, nil, []byte(`{"model":"m","input":"hi"}`))
		n := normalized(lastCall(t, session))
		if n["output_text"] != "ok" {
			t.Fatalf("output_text=%v", n["output_text"])
		}
		if !hasParserWarning(n, "trailing_data_after_json") {
			t.Fatalf("warnings=%v", n["parser_warnings"])
		}
		if !bytes.Contains(client, []byte("trailing")) {
			t.Fatal("client bytes changed")
		}
	})
	t.Run("sse_data_trailing_junk", func(t *testing.T) {
		stream := "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]} junk\n\ndata: [DONE]\n\n"
		up := func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, stream)
		}
		p := startSpecProxy(t, "warn", "relaxed", up)
		sid := newSession()
		_, _, client, _ := postRaw(t, p, "/v1/chat/completions", sid, nil, []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"stream":true}`))
		if !bytes.Equal(client, []byte(stream)) {
			t.Fatalf("client bytes changed")
		}
	})
	t.Run("two_concatenated_chat_objects", func(t *testing.T) {
		up := func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"c","model":"m","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}{"x":1}`))
		}
		p := startSpecProxy(t, "warn", "relaxed", up)
		sid := newSession()
		_, _, client, session := postRaw(t, p, "/v1/chat/completions", sid, nil, []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`))
		n := normalized(lastCall(t, session))
		if n["output_text"] != "ok" {
			t.Fatalf("output_text=%v", n["output_text"])
		}
		if !hasParserWarning(n, "trailing_data_after_json") {
			t.Fatalf("warnings=%v", n["parser_warnings"])
		}
		if !bytes.Contains(client, []byte(`{"x":1}`)) {
			t.Fatal("client bytes changed")
		}
	})
}

func TestH1EmptyAndConcatenatedParserWarnings(t *testing.T) {
	bodies := [][]byte{
		[]byte(`{}`),
		[]byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}{"x":1}`),
	}
	for _, mode := range []string{"off", "warn", "enforce"} {
		for _, body := range bodies {
			t.Run(fmt.Sprintf("%s/%s", mode, body[:2]), func(t *testing.T) {
				p := startSpecProxy(t, mode, "relaxed", chatOK())
				sid := newSession()
				_, _, _, session := postRaw(t, p, "/v1/chat/completions", sid, nil, body)
				warns := asAnySlice(normalized(lastCall(t, session))["parser_warnings"])
				if len(warns) == 0 {
					t.Fatalf("mode=%s body=%s warnings empty", mode, body)
				}
			})
		}
	}
}

func TestI1Scale50x20(t *testing.T) {
	up := func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "responses") {
			responsesOK()(w, r)
			return
		}
		chatOK()(w, r)
	}
	p := startSpecProxy(t, "warn", "strict", up)
	cleanChat := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
	badChat := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"foo":1}`)
	enumChat := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"shirt","parameters":{"type":"object","properties":{"color":{"type":"string","enum":["red"],"enumDescriptions":{"red":"warm"}}}}}}]}`)
	cleanResp := []byte(`{"model":"m","input":"hi"}`)
	var (
		mu   sync.Mutex
		errs []string
		wg   sync.WaitGroup
	)
	report := func(msg string) {
		mu.Lock()
		errs = append(errs, msg)
		mu.Unlock()
	}
	for s := 0; s < 50; s++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sid := newSession()
			for i := 0; i < 20; i++ {
				var (
					path string
					body []byte
					kind string
				)
				switch i % 5 {
				case 0:
					path, body, kind = "/v1/chat/completions", cleanChat, "clean_chat"
				case 1:
					path, body, kind = "/v1/chat/completions", badChat, "bad_chat"
				case 2:
					path, body, kind = "/v1/chat/completions", enumChat, "enum_chat"
				case 3:
					path, body, kind = "/v1/responses", cleanResp, "clean_resp"
				default:
					path, body, kind = "/v1/responses", []byte(`{"model":"m","input":"hi","foo":1}`), "bad_resp"
				}
				_, _, _, session := postRaw(t, p, path, sid, nil, body)
				n := normalized(lastCall(t, session))
				switch kind {
				case "clean_chat", "clean_resp":
					if requestViolations(n) != 0 {
						report(sid + " " + kind + " has violations")
					}
				case "bad_chat", "bad_resp":
					if requestViolations(n) == 0 {
						report(sid + " " + kind + " missing violations")
					}
				case "enum_chat":
					if !hasIgnoredKey(n, "enumDescriptions") {
						report(sid + " missing enumDescriptions")
					}
				}
			}
		}()
	}
	wg.Wait()
	if len(errs) > 0 {
		t.Fatalf("cross-talk/scale errors (%d): %s", len(errs), strings.Join(errs[:min(len(errs), 8)], "; "))
	}
}

func TestB10StrictTrueWithoutAdditionalProperties(t *testing.T) {
	t.Run("strict_true", func(t *testing.T) {
		body := []byte(`{"model":"m","input":"hi","tools":[{"type":"function","name":"x","strict":true,"parameters":{"type":"object","properties":{"a":{"type":"string"}}}}]}`)
		p := startSpecProxy(t, "warn", "relaxed", responsesOK())
		sid := newSession()
		_, _, _, session := postRaw(t, p, "/v1/responses", sid, nil, body)
		if !hasViolationSource(normalized(lastCall(t, session)), "strict_mode", "additionalProperties") {
			t.Fatalf("%v", normalized(lastCall(t, session))["spec_violations"])
		}
	})
	t.Run("omitted_strict", func(t *testing.T) {
		body := []byte(`{"model":"m","input":"hi","tools":[{"type":"function","name":"x","parameters":{"type":"object","properties":{"a":{"type":"string"}}}}]}`)
		p := startSpecProxy(t, "warn", "relaxed", responsesOK())
		sid := newSession()
		_, _, _, session := postRaw(t, p, "/v1/responses", sid, nil, body)
		assertIgnoredKind(t, normalized(lastCall(t, session)), "strict_fallback_predicted")
	})
}

func TestOverlayChatMessageExtraProps(t *testing.T) {
	body := []byte(`{"model":"m","messages":[{"role":"user","content":"hi","foo":1}]}`)
	t.Run("strict_flags", func(t *testing.T) {
		p := startSpecProxy(t, "warn", "strict", chatOK())
		sid := newSession()
		_, _, _, session := postRaw(t, p, "/v1/chat/completions", sid, nil, body)
		n := normalized(lastCall(t, session))
		found := false
		for _, v := range asAnySlice(n["spec_violations"]) {
			m := v.(map[string]any)
			if m["side"] == "response" {
				continue
			}
			loc := fmt.Sprint(m["pointer"]) + " " + fmt.Sprint(m["path"]) + " " + fmt.Sprint(m["message"])
			if strings.Contains(loc, "messages") && (strings.Contains(loc, "foo") || m["keyword"] == "unevaluatedProperties") {
				found = true
			}
		}
		if !found {
			t.Fatalf("strict should flag /messages/0/foo: %v", n["spec_violations"])
		}
	})
	t.Run("relaxed_does_not_flag_violation", func(t *testing.T) {
		p := startSpecProxy(t, "warn", "relaxed", chatOK())
		sid := newSession()
		_, _, _, session := postRaw(t, p, "/v1/chat/completions", sid, nil, body)
		n := normalized(lastCall(t, session))
		if requestViolations(n) != 0 {
			t.Fatalf("relaxed should not spec-violate message extras: %v", n["spec_violations"])
		}
		assertIgnoredKind(t, n, "unknown_request_field")
	})
}

func hasIgnoredKey(n map[string]any, key string) bool {
	for _, v := range asAnySlice(n["ignored_fields"]) {
		m, _ := v.(map[string]any)
		if m["key"] == key {
			return true
		}
	}
	return false
}

func hasIgnoredContext(n map[string]any, context string) bool {
	for _, v := range asAnySlice(n["ignored_fields"]) {
		m, _ := v.(map[string]any)
		if m["context"] == context {
			return true
		}
	}
	return false
}

func hasViolationKeyword(n map[string]any, keyword string) bool {
	for _, v := range asAnySlice(n["spec_violations"]) {
		m, _ := v.(map[string]any)
		if m["keyword"] == keyword {
			return true
		}
	}
	return false
}

func hasViolationSource(n map[string]any, source, keyword string) bool {
	for _, v := range asAnySlice(n["spec_violations"]) {
		m, _ := v.(map[string]any)
		if m["source"] == source && (keyword == "" || m["keyword"] == keyword) {
			return true
		}
	}
	return false
}

func requestViolations(n map[string]any) int {
	nreq := 0
	for _, v := range asAnySlice(n["spec_violations"]) {
		m, _ := v.(map[string]any)
		if m["side"] == "response" {
			continue
		}
		nreq++
	}
	return nreq
}

func hasParserWarning(n map[string]any, substr string) bool {
	for _, w := range asAnySlice(n["parser_warnings"]) {
		if strings.Contains(fmt.Sprint(w), substr) {
			return true
		}
	}
	return false
}

func sizedChatBody(n int) []byte {
	prefix := `{"model":"m","messages":[{"role":"user","content":"`
	suffix := `"}]}`
	pad := n - len(prefix) - len(suffix)
	if pad < 0 {
		pad = 0
	}
	return []byte(prefix + strings.Repeat("x", pad) + suffix)
}

func strictToolBody(schema map[string]any) []byte {
	body, _ := json.Marshal(map[string]any{
		"model":    "m",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
		"tools": []any{map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":       "x",
				"strict":     true,
				"parameters": schema,
			},
		}},
	})
	return body
}

func measuredNesting(depth int) map[string]any {
	if depth <= 1 {
		return map[string]any{"type": "object", "additionalProperties": false}
	}
	cur := map[string]any{"type": "string"}
	for i := 0; i < depth-2; i++ {
		cur = map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties":           map[string]any{"c": cur},
			"required":             []any{"c"},
		}
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           map[string]any{"c": cur},
		"required":             []any{"c"},
	}
}

func nPropSchema(n int) map[string]any {
	props := make(map[string]any, n)
	req := make([]any, 0, n)
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("p%d", i)
		props[name] = map[string]any{"type": "string"}
		req = append(req, name)
	}
	return map[string]any{"type": "object", "additionalProperties": false, "properties": props, "required": req}
}

func enumCountSchema(count, valueLen int) map[string]any {
	vals := make([]any, count)
	for i := 0; i < count; i++ {
		vals[i] = fmt.Sprintf("%s%04d", strings.Repeat("v", valueLen), i)
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           map[string]any{"color": map[string]any{"type": "string", "enum": vals}},
		"required":             []any{"color"},
	}
}

func streamP95(t *testing.T, p proxyProc, body []byte) time.Duration {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, p.proxyURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(lab.SessionHeaderName, newSession())
	client := &http.Client{Transport: &http.Transport{DisableCompression: true}}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	buf := make([]byte, 64)
	var stamps []time.Time
	for {
		n, err := res.Body.Read(buf)
		if n > 0 {
			stamps = append(stamps, time.Now())
		}
		if err != nil {
			break
		}
	}
	if len(stamps) < 3 {
		t.Fatalf("too few chunks: %d", len(stamps))
	}
	deltas := make([]time.Duration, 0, len(stamps)-1)
	for i := 1; i < len(stamps); i++ {
		deltas = append(deltas, stamps[i].Sub(stamps[i-1]))
	}
	return percentileDuration(deltas, 0.95)
}

func percentileDuration(values []time.Duration, p float64) time.Duration {
	if len(values) == 0 {
		return 0
	}
	cp := append([]time.Duration(nil), values...)
	for i := 0; i < len(cp); i++ {
		for j := i + 1; j < len(cp); j++ {
			if cp[j] < cp[i] {
				cp[i], cp[j] = cp[j], cp[i]
			}
		}
	}
	idx := int(float64(len(cp)-1) * p)
	if idx < 0 {
		idx = 0
	}
	if idx >= len(cp) {
		idx = len(cp) - 1
	}
	return cp[idx]
}
