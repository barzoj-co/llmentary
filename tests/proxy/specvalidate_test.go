// SPDX-License-Identifier: AGPL-3.0-only

package proxy_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestA1Defaults(t *testing.T) {
	p := startSpecProxy(t, "", "", chatOK())
	cfg := getConfig(t, p.controlURL)
	if cfg["spec_validation"] != "warn" || cfg["spec_profile"] != "relaxed" {
		t.Fatalf("config=%v", cfg)
	}
	sid := newSession()
	_, _, _, session := postRaw(t, p, "/v1/chat/completions", sid, nil, []byte(`{"model":"gpt-fixture","messages":[{"role":"user","content":"hi"}]}`))
	val := normalized(lastCall(t, session))["validation"].(map[string]any)
	if val["mode"] != "warn" || val["profile"] != "relaxed" {
		t.Fatalf("validation=%v", val)
	}
}

func TestA2FlagsAndEnv(t *testing.T) {
	t.Run("cli_and_env", func(t *testing.T) {
		p := startSpecProxy(t, "enforce", "strict", chatOK())
		cfg := getConfig(t, p.controlURL)
		if cfg["spec_validation"] != "enforce" || cfg["spec_profile"] != "strict" {
			t.Fatalf("config=%v", cfg)
		}
	})
	t.Run("invalid_mode", func(t *testing.T) {
		cmd := exec.Command(proxyBinary(t), "--spec-validation=strict", "--upstream", "https://example.com")
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatal("expected non-zero exit")
		}
		if !bytes.Contains(out, []byte("--spec-validation")) || !bytes.Contains(out, []byte("off, warn, enforce")) {
			t.Fatalf("message=%s", out)
		}
	})
	t.Run("invalid_profile", func(t *testing.T) {
		cmd := exec.Command(proxyBinary(t), "--spec-profile=lenient", "--upstream", "https://example.com")
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatal("expected non-zero exit")
		}
		if !bytes.Contains(out, []byte("--spec-profile")) || !bytes.Contains(out, []byte("strict, relaxed")) {
			t.Fatalf("message=%s", out)
		}
	})
	t.Run("negative_max_bytes", func(t *testing.T) {
		cmd := exec.Command(proxyBinary(t), "--spec-validation-max-bytes=-1", "--upstream", "https://example.com")
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatal("expected non-zero exit")
		}
		if !bytes.Contains(out, []byte("--spec-validation-max-bytes")) {
			t.Fatalf("message=%s", out)
		}
	})
}

func TestA4OffOmitsFields(t *testing.T) {
	p := startSpecProxy(t, "off", "relaxed", chatOK())
	bodies := [][]byte{
		[]byte(`{"model":"gpt-fixture","messages":[{"role":"user","content":"hi"}]}`),
		[]byte(`{"model":"gpt-fixture","messages":[{"role":"user","content":"hi"}],"foo":1}`),
		[]byte(`{"model":"gpt-fixture"}`),
		[]byte(`{}`),
		[]byte(`{"model":"gpt-fixture","messages":[{"role":"user","content":"hi"}]} xyz`),
		[]byte(`{"model":"m","messages":[{"role":"user","content":"hi","foo":1}]}`),
		[]byte(`{"model":"m","input":"hi"}`),
		[]byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"x","parameters":{},"foo":1}}]}`),
		[]byte(`{"model":"m","input":"hi","tools":[{"type":"function","name":"x","parameters":{}}]}`),
		[]byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"temperature":"0.7"}`),
	}
	paths := []string{"/v1/chat/completions", "/v1/chat/completions", "/v1/chat/completions", "/v1/chat/completions", "/v1/chat/completions", "/v1/chat/completions", "/v1/responses", "/v1/chat/completions", "/v1/responses", "/v1/chat/completions"}
	for i, body := range bodies {
		sid := newSession()
		_, _, _, session := postRaw(t, p, paths[i], sid, nil, body)
		raw := sessionRaw(t, p.controlURL, sid)
		if rawJSONHasKey(raw, "validation") || rawJSONHasKey(raw, "spec_violations") || rawJSONHasKey(raw, "ignored_fields") {
			t.Fatalf("off mode leaked keys in %s", raw)
		}
		_ = session
	}
}

func TestA5WarnEmptyArraysPresent(t *testing.T) {
	p := startSpecProxy(t, "warn", "relaxed", chatOK())
	sid := newSession()
	_, _, _, _ = postRaw(t, p, "/v1/chat/completions", sid, nil, []byte(`{"model":"gpt-fixture","messages":[{"role":"user","content":"hi"}]}`))
	raw := sessionRaw(t, p.controlURL, sid)
	if !bytes.Contains(raw, []byte(`"spec_violations":[]`)) || !bytes.Contains(raw, []byte(`"ignored_fields":[]`)) {
		t.Fatalf("empty arrays missing: %s", raw)
	}
	var session map[string]any
	_ = json.Unmarshal(raw, &session)
	val := normalized(lastCall(t, session))["validation"].(map[string]any)
	if val["request_checked"] != true || val["response_checked"] != true {
		t.Fatalf("checked flags=%v", val)
	}
}

func TestBMatrix(t *testing.T) {
	type row struct {
		name, route       string
		body              []byte
		strictV, relaxedV string
		ignoredKind       string
		jsonPath, pointer string
	}
	chat := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"foo":1}`)
	chatMsg := []byte(`{"model":"m","messages":[{"role":"user","content":"hi","foo":1}]}`)
	respFoo := []byte(`{"model":"m","input":"hi","foo":1}`)
	respIn := []byte(`{"model":"m","input":[{"role":"user","content":"hi","foo":1}]}`)
	chatTool := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"x","parameters":{"type":"object"},"foo":1}}]}`)
	respTool := []byte(`{"model":"m","input":"hi","tools":[{"type":"function","name":"x","parameters":{"type":"object"},"foo":1}]}`)
	respNoStrict := []byte(`{"model":"m","input":"hi","tools":[{"type":"function","name":"x","parameters":{"type":"object","properties":{"a":{"type":"string"}}}}]}`)
	badEnumChat := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"tool_choice":"maybe"}`)
	badEnumResp := []byte(`{"model":"m","input":"hi","tool_choice":"maybe"}`)
	missingChat := []byte(`{"model":"m"}`)
	missingResp := []byte(`{"input":"hi"}`)
	empty := []byte(`{}`)
	wrongType := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"temperature":"0.7"}`)
	wrongTypeResp := []byte(`{"model":"m","input":"hi","temperature":"0.7"}`)
	trail := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]} xyz`)
	two := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}{"x":1}`)
	twoSpace := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]} {"x":1}`)
	trailResp := []byte(`{"model":"m","input":"hi"} xyz`)

	rows := []row{
		{"B1_chat", "/v1/chat/completions", chat, "unevaluatedProperties", "unevaluatedProperties", "", "foo", "/foo"},
		{"B1_resp", "/v1/responses", respFoo, "unevaluatedProperties", "unevaluatedProperties", "", "foo", "/foo"},
		{"B2_chat_msg", "/v1/chat/completions", chatMsg, "unevaluatedProperties", "", "unknown_request_field", "messages[0].foo", "/messages/0/foo"},
		{"B3_resp_input", "/v1/responses", respIn, "unevaluatedProperties", "unevaluatedProperties", "", "input[0].foo", "/input/0/foo"},
		{"B4_chat_tool", "/v1/chat/completions", chatTool, "unevaluatedProperties", "", "unknown_request_field", "tools[0].function.foo", "/tools/0/function/foo"},
		{"B4_resp_tool", "/v1/responses", respTool, "unevaluatedProperties", "", "unknown_request_field", "tools[0].foo", "/tools/0/foo"},
		{"B5_resp_no_strict", "/v1/responses", respNoStrict, "required", "", "strict_fallback_predicted", "tools[0]", "/tools/0"},
		{"B6_chat_enum", "/v1/chat/completions", badEnumChat, "enum", "enum", "", "tool_choice", "/tool_choice"},
		{"B6_resp_enum", "/v1/responses", badEnumResp, "enum", "enum", "", "tool_choice", "/tool_choice"},
		{"B7_chat_missing", "/v1/chat/completions", missingChat, "required", "required", "", "messages", ""},
		{"B7_resp_missing", "/v1/responses", missingResp, "required", "required", "", "model", ""},
		{"B7_empty_chat", "/v1/chat/completions", empty, "required", "required", "", "messages", ""},
		{"B7_empty_resp", "/v1/responses", empty, "required", "required", "", "model", ""},
		{"B8_type_chat", "/v1/chat/completions", wrongType, "type", "type", "", "temperature", "/temperature"},
		{"B8_type_resp", "/v1/responses", wrongTypeResp, "type", "type", "", "temperature", "/temperature"},
		{"B9_trail", "/v1/chat/completions", trail, "trailingData", "trailingData", "", "", ""},
		{"B9_two", "/v1/chat/completions", two, "trailingData", "trailingData", "", "", ""},
		{"B9_two_space", "/v1/chat/completions", twoSpace, "trailingData", "trailingData", "", "", ""},
		{"B9_trail_resp", "/v1/responses", trailResp, "trailingData", "trailingData", "", "", ""},
	}

	for _, r := range rows {
		for _, profile := range []string{"strict", "relaxed"} {
			for _, mode := range []string{"off", "warn", "enforce"} {
				t.Run(r.name+"/profile="+profile+"/mode="+mode, func(t *testing.T) {
					up := chatOK()
					if strings.Contains(r.route, "responses") {
						up = responsesOK()
					}
					p := startSpecProxy(t, mode, profile, up)
					sid := newSession()
					status, hdr, body, session := postRaw(t, p, r.route, sid, nil, r.body)
					call := lastCall(t, session)
					wantKeyword := r.relaxedV
					if profile == "strict" {
						wantKeyword = r.strictV
					}
					hasViolation := wantKeyword != ""
					hasIgnored := r.ignoredKind != ""
					shouldReject := mode == "enforce" && (hasViolation || hasIgnored)
					switch mode {
					case "off":
						if p.upstream.Count() != 1 {
							t.Fatalf("upstream count=%d", p.upstream.Count())
						}
						raw := sessionRaw(t, p.controlURL, sid)
						if rawJSONHasKey(raw, "validation") {
							t.Fatal("off leaked validation")
						}
					case "warn":
						if p.upstream.Count() != 1 {
							t.Fatalf("upstream=%d", p.upstream.Count())
						}
						if !bytes.Equal(p.upstream.LastBody(), r.body) {
							t.Fatalf("bytes mutated")
						}
						n := normalized(call)
						assertKeyword(t, n, wantKeyword)
						assertIgnoredKind(t, n, r.ignoredKind)
						assertBMatrixLocation(t, n, r.jsonPath, r.pointer, wantKeyword, r.ignoredKind, r.body)
					case "enforce":
						if shouldReject {
							if status != 400 {
								t.Fatalf("status=%d body=%s", status, body)
							}
							if hdr.Get("X-LLMTestLab-Rejected") == "" {
								t.Fatal("missing reject header")
							}
							if p.upstream.Count() != 0 {
								t.Fatalf("upstream called")
							}
							if call["upstream_called"] != false {
								t.Fatalf("upstream_called=%v", call["upstream_called"])
							}
							n := normalized(call)
							errObj, _ := n["error"].(map[string]any)
							if errObj["source"] != "proxy" {
								t.Fatalf("error=%v", errObj)
							}
							assertBMatrixLocation(t, n, r.jsonPath, r.pointer, wantKeyword, r.ignoredKind, r.body)
							assertEnforceError(t, body, profile, hasViolation, n)
						} else if p.upstream.Count() != 1 {
							t.Fatalf("expected forward, count=%d", p.upstream.Count())
						}
					}
				})
			}
		}
	}
}

func assertBMatrixLocation(t *testing.T, n map[string]any, jsonPath, pointer, keyword, ignoredKind string, body []byte) {
	t.Helper()
	if keyword != "" {
		var match map[string]any
		for _, v := range asAnySlice(n["spec_violations"]) {
			m, _ := v.(map[string]any)
			if m["side"] == "response" || m["keyword"] != keyword {
				continue
			}
			if pointer != "" && m["pointer"] != pointer {
				continue
			}
			match = m
			break
		}
		if match == nil && pointer != "" {
			for _, v := range asAnySlice(n["spec_violations"]) {
				m, _ := v.(map[string]any)
				if m["side"] == "response" || m["keyword"] != keyword {
					continue
				}
				if m["pointer"] == pointer || strings.HasSuffix(fmt.Sprint(m["pointer"]), pointer) {
					match = m
					break
				}
			}
		}
		if match == nil {
			t.Fatalf("missing keyword %s pointer %s in %v", keyword, pointer, n["spec_violations"])
		}
		if pointer != "" && match["pointer"] != pointer {
			t.Fatalf("violation pointer=%v want %s (%v)", match["pointer"], pointer, match)
		}
		if jsonPath != "" && match["path"] != jsonPath && !strings.Contains(fmt.Sprint(match["path"]), jsonPath) && !strings.Contains(fmt.Sprint(match["message"]), jsonPath) {
			t.Fatalf("violation path=%v want %s (%v)", match["path"], jsonPath, match)
		}
		if keyword == "trailingData" {
			wantOff := trailingJSONOffset(body)
			gotOff, ok := asInt(match["offset"])
			if !ok || gotOff != wantOff {
				t.Fatalf("B9 offset=%v want %d (%v)", match["offset"], wantOff, match)
			}
		}
	}
	if ignoredKind != "" && jsonPath != "" {
		found := false
		for _, v := range asAnySlice(n["ignored_fields"]) {
			m, _ := v.(map[string]any)
			if m["kind"] != ignoredKind {
				continue
			}
			if pointer != "" && m["pointer"] != pointer && !strings.Contains(fmt.Sprint(m["pointer"]), pointer) {
				continue
			}
			found = true
			if pointer != "" && m["pointer"] != pointer && !strings.Contains(fmt.Sprint(m["pointer"]), pointer) {
				t.Fatalf("ignored pointer=%v want %s", m["pointer"], pointer)
			}
			if m["path"] != jsonPath && !strings.Contains(fmt.Sprint(m["path"]), jsonPath) {
				t.Fatalf("ignored path=%v want %s", m["path"], jsonPath)
			}
			break
		}
		if !found && keyword == "" {
			t.Fatalf("missing ignored %s at %s: %v", ignoredKind, jsonPath, n["ignored_fields"])
		}
	}
}

func assertEnforceError(t *testing.T, body []byte, profile string, hasViolation bool, n map[string]any) {
	t.Helper()
	var env map[string]any
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("enforce body: %v %s", err, body)
	}
	errObj, _ := env["error"].(map[string]any)
	wantCode := "llmtestlab_ignored_field"
	if hasViolation {
		wantCode = "llmtestlab_spec_violation"
	}
	if fmt.Sprint(errObj["code"]) != wantCode {
		t.Fatalf("enforce code=%v want %s body=%s", errObj["code"], wantCode, body)
	}
	if fmt.Sprint(errObj["type"]) != "invalid_request_error" {
		t.Fatalf("enforce type=%v", errObj["type"])
	}
	msg := fmt.Sprint(errObj["message"])
	if !strings.Contains(msg, "llm-test-lab: request rejected (profile="+profile+")") {
		t.Fatalf("enforce message=%q", msg)
	}
	param, pointer := firstRejectLocation(n, hasViolation)
	if fmt.Sprint(errObj["param"]) != param {
		t.Fatalf("enforce param=%v want %s", errObj["param"], param)
	}
	if pointer != "" && !strings.Contains(msg, pointer) {
		t.Fatalf("enforce message missing pointer %s: %q", pointer, msg)
	}
}

func firstRejectLocation(n map[string]any, hasViolation bool) (param, pointer string) {
	if hasViolation {
		for _, v := range asAnySlice(n["spec_violations"]) {
			m, _ := v.(map[string]any)
			if m["side"] == "response" {
				continue
			}
			return fmt.Sprint(m["path"]), fmt.Sprint(m["pointer"])
		}
	}
	for _, v := range asAnySlice(n["ignored_fields"]) {
		m, _ := v.(map[string]any)
		if m["action"] == "report_only" || m["severity"] == "info" {
			continue
		}
		return fmt.Sprint(m["path"]), fmt.Sprint(m["pointer"])
	}
	return "", ""
}

func asInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	case json.Number:
		i, err := n.Int64()
		return int(i), err == nil
	default:
		return 0, false
	}
}

func TestResponsesInputTypeNumber(t *testing.T) {
	body := []byte(`{"model":"m","input":123}`)
	p := startSpecProxy(t, "warn", "relaxed", responsesOK())
	sid := newSession()
	_, _, _, session := postRaw(t, p, "/v1/responses", sid, nil, body)
	n := normalized(lastCall(t, session))
	found := false
	for _, v := range asAnySlice(n["spec_violations"]) {
		m, _ := v.(map[string]any)
		if m["keyword"] == "type" && (m["pointer"] == "/input" || m["path"] == "input") {
			found = true
		}
	}
	if !found {
		t.Fatalf("input:123 must produce /input type: %v", n["spec_violations"])
	}
}

func TestB9WhitespaceOK(t *testing.T) {
	for _, body := range [][]byte{
		[]byte("{\"model\":\"m\",\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}]}\n"),
		[]byte("{\"model\":\"m\",\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}]}\r\n"),
		[]byte("{\"model\":\"m\",\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}]}   "),
	} {
		p := startSpecProxy(t, "warn", "strict", chatOK())
		sid := newSession()
		_, _, _, session := postRaw(t, p, "/v1/chat/completions", sid, nil, body)
		assertKeyword(t, normalized(lastCall(t, session)), "")
	}
}

func TestDEnumDescriptions(t *testing.T) {
	objectForm := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"shirt","parameters":{"type":"object","properties":{"color":{"type":"string","enum":["red","blue"],"description":"Shirt color","enumDescriptions":{"red":"warm tone","blue":"cool tone"}}},"required":["color"]}}}]}`)
	arrayForm := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"shirt","parameters":{"type":"object","properties":{"color":{"type":"string","enum":["red","blue"],"enum_descriptions":["warm tone","cool tone"]}}}}}]}`)
	forum := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"response_format":{"type":"json_schema","json_schema":{"name":"safety","strict":true,"schema":{"type":"object","properties":{"detection":{"type":"string","description":"Classification code for safety policy detection.","enum":["n","i","h","s"],"enumDescriptions":{"n":"No detection of safety policy issues.","i":"Illegal and dangerous activities.","h":"Harmful and violent content.","s":"Sexual explicit materials."}}},"required":["detection"],"additionalProperties":false}}}}`)
	p := startSpecProxy(t, "warn", "relaxed", chatOK())
	hintEnum := "OpenAI does not read `enumDescriptions`; the model never sees it. Move the per-value descriptions into this property's `description`."
	hintSnake := "OpenAI does not read `enum_descriptions`; the model never sees it. Move the per-value descriptions into this property's `description`."
	for name, body := range map[string][]byte{"D1": objectForm, "D2": arrayForm, "D5": forum} {
		t.Run(name, func(t *testing.T) {
			sid := newSession()
			_, _, _, session := postRaw(t, p, "/v1/chat/completions", sid, nil, body)
			n := normalized(lastCall(t, session))
			switch name {
			case "D1":
				assertIgnoredExact(t, n, map[string]any{
					"path":    "tools[0].function.parameters.properties.color.enumDescriptions",
					"pointer": "/tools/0/function/parameters/properties/color/enumDescriptions",
					"key":     "enumDescriptions", "kind": "unknown_schema_keyword",
					"context": "chat.tools.function.parameters", "effective_strict": false,
					"upstream_behavior": "silently_ignored", "hint": hintEnum,
					"suggested_description": "Shirt color. red: warm tone; blue: cool tone",
				})
			case "D2":
				assertIgnoredExact(t, n, map[string]any{
					"path":    "tools[0].function.parameters.properties.color.enum_descriptions",
					"pointer": "/tools/0/function/parameters/properties/color/enum_descriptions",
					"key":     "enum_descriptions", "kind": "unknown_schema_keyword",
					"context": "chat.tools.function.parameters", "effective_strict": false,
					"upstream_behavior": "silently_ignored", "hint": hintSnake,
					"suggested_description": "red: warm tone; blue: cool tone",
				})
			case "D5":
				assertIgnoredExact(t, n, map[string]any{
					"path":    "response_format.json_schema.schema.properties.detection.enumDescriptions",
					"pointer": "/response_format/json_schema/schema/properties/detection/enumDescriptions",
					"key":     "enumDescriptions", "kind": "unknown_schema_keyword",
					"context": "chat.response_format.json_schema", "effective_strict": true,
					"upstream_behavior": "silently_ignored", "hint": hintEnum,
					"suggested_description": "Classification code for safety policy detection. n: No detection of safety policy issues.; i: Illegal and dangerous activities.; h: Harmful and violent content.; s: Sexual explicit materials.",
				})
			}
		})
	}
}

func TestD7FalsePositives(t *testing.T) {
	body := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"x","parameters":{"type":"object","properties":{"enumDescriptions":{"type":"string"},"note":{"type":"string","enum":["enumDescriptions"],"default":{"oneOf":1},"description":"mentions enumDescriptions"}}}}}]}`)
	p := startSpecProxy(t, "warn", "relaxed", chatOK())
	sid := newSession()
	_, _, _, session := postRaw(t, p, "/v1/chat/completions", sid, nil, body)
	ignored, _ := normalized(lastCall(t, session))["ignored_fields"].([]any)
	if len(ignored) != 0 {
		t.Fatalf("false positives=%v", ignored)
	}
}

func TestD9Modes(t *testing.T) {
	body := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"shirt","parameters":{"type":"object","properties":{"color":{"type":"string","enum":["red"],"enumDescriptions":{"red":"warm"}}}}}}]}`)
	for _, profile := range []string{"strict", "relaxed"} {
		t.Run("warn/"+profile, func(t *testing.T) {
			p := startSpecProxy(t, "warn", profile, chatOK())
			sid := newSession()
			_, _, clientBody, _ := postRaw(t, p, "/v1/chat/completions", sid, nil, body)
			if p.upstream.Count() != 1 || !bytes.Equal(p.upstream.LastBody(), body) {
				t.Fatal("not forwarded byte-for-byte")
			}
			if !bytes.Contains(clientBody, []byte(`"ok"`)) {
				t.Fatalf("client=%s", clientBody)
			}
		})
		t.Run("enforce/"+profile, func(t *testing.T) {
			p := startSpecProxy(t, "enforce", profile, chatOK())
			sid := newSession()
			status, hdr, resp, session := postRaw(t, p, "/v1/chat/completions", sid, nil, body)
			if status != 400 || hdr.Get("X-LLMTestLab-Rejected") != "ignored_field" {
				t.Fatalf("status=%d hdr=%v body=%s", status, hdr, resp)
			}
			if p.upstream.Count() != 0 {
				t.Fatal("upstream called")
			}
			var env map[string]any
			_ = json.Unmarshal(resp, &env)
			errObj := env["error"].(map[string]any)
			if errObj["code"] != "llmtestlab_ignored_field" {
				t.Fatalf("error=%v", errObj)
			}
			_ = session
		})
		t.Run("off/"+profile, func(t *testing.T) {
			p := startSpecProxy(t, "off", profile, chatOK())
			sid := newSession()
			postRaw(t, p, "/v1/chat/completions", sid, nil, body)
			raw := sessionRaw(t, p.controlURL, sid)
			if rawJSONHasKey(raw, "ignored_fields") {
				t.Fatal("off leaked fields")
			}
		})
	}
}

func TestD9aClientContract(t *testing.T) {
	body := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"shirt","parameters":{"type":"object","properties":{"color":{"type":"string","enum":["red"],"enumDescriptions":{"red":"warm"}}}}}}]}`)
	clean := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
	pWarn := startSpecProxy(t, "warn", "relaxed", chatOK())
	sid := newSession()
	_, _, _, session := postRaw(t, pWarn, "/v1/chat/completions", sid, nil, body)
	if err := assertNoIgnoredFields(session, false); err == nil {
		t.Fatal("expected failure")
	} else if !strings.Contains(err.Error(), "enumDescriptions") {
		t.Fatalf("message=%v", err)
	}
	sid2 := newSession()
	_, _, _, cleanS := postRaw(t, pWarn, "/v1/chat/completions", sid2, nil, clean)
	if err := assertNoIgnoredFields(cleanS, false); err != nil {
		t.Fatal(err)
	}
	pOff := startSpecProxy(t, "off", "relaxed", chatOK())
	sid3 := newSession()
	_, _, _, offS := postRaw(t, pOff, "/v1/chat/completions", sid3, nil, clean)
	if err := assertNoIgnoredFields(offS, false); err == nil || !strings.Contains(err.Error(), "validation was not run") {
		t.Fatalf("off: %v", err)
	}
}

func assertNoIgnoredFields(session map[string]any, allowUnvalidated bool) error {
	calls, _ := session["calls"].([]any)
	for i, raw := range calls {
		call, _ := raw.(map[string]any)
		n, _ := call["normalized"].(map[string]any)
		if n == nil {
			if allowUnvalidated {
				continue
			}
			return fmtErr("%d unknown validation was not run", i+1)
		}
		if _, ok := n["validation"]; !ok {
			if allowUnvalidated {
				continue
			}
			return fmtErr("%d unknown validation was not run", i+1)
		}
		val, _ := n["validation"].(map[string]any)
		if val["mode"] == "off" || val["request_checked"] == false {
			if allowUnvalidated {
				continue
			}
			return fmtErr("%d unknown validation was not run", i+1)
		}
		ignored, _ := n["ignored_fields"].([]any)
		for _, item := range ignored {
			f, _ := item.(map[string]any)
			if f["severity"] == "info" {
				continue
			}
			family, _ := call["api_family"].(string)
			return fmtErr("%d %s request %s: %s — %s", i+1, family, f["path"], f["kind"], f["hint"])
		}
	}
	return nil
}

func fmtErr(format string, args ...any) error {
	return fmt.Errorf(format, args...)
}

func TestF4OddTopLevel(t *testing.T) {
	cases := map[string][]byte{
		"empty": []byte(""),
		"null":  []byte("null"),
		"array": []byte("[]"),
		"str":   []byte(`"x"`),
		"num":   []byte("42"),
	}
	want := map[string]string{"empty": "emptyBody", "null": "type", "array": "type", "str": "type", "num": "type"}
	for name, body := range cases {
		p := startSpecProxy(t, "warn", "strict", chatOK())
		sid := newSession()
		_, _, _, session := postRaw(t, p, "/v1/chat/completions", sid, nil, body)
		assertKeyword(t, normalized(lastCall(t, session)), want[name])
		if p.upstream.Count() != 1 {
			t.Fatalf("%s not forwarded", name)
		}
		p2 := startSpecProxy(t, "enforce", "strict", chatOK())
		sid2 := newSession()
		status, _, _, _ := postRaw(t, p2, "/v1/chat/completions", sid2, nil, body)
		if status != 400 || p2.upstream.Count() != 0 {
			t.Fatalf("%s enforce status=%d up=%d", name, status, p2.upstream.Count())
		}
	}
}

func TestF5DuplicateKeys(t *testing.T) {
	body := []byte(`{"model":"a","model":"b","messages":[{"role":"user","content":"hi"}]}`)
	p := startSpecProxy(t, "warn", "strict", chatOK())
	sid := newSession()
	_, _, _, session := postRaw(t, p, "/v1/chat/completions", sid, nil, body)
	assertKeyword(t, normalized(lastCall(t, session)), "duplicateKey")
	p2 := startSpecProxy(t, "warn", "relaxed", chatOK())
	sid2 := newSession()
	_, _, _, session2 := postRaw(t, p2, "/v1/chat/completions", sid2, nil, body)
	assertKeyword(t, normalized(lastCall(t, session2)), "duplicateKey")
}

func TestF6Numbers(t *testing.T) {
	p := startSpecProxy(t, "warn", "strict", chatOK())
	sid := newSession()
	_, _, _, session := postRaw(t, p, "/v1/chat/completions", sid, nil, []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"seed":1e400}`))
	n := normalized(lastCall(t, session))
	assertKeyword(t, n, "numberRange")
}

func TestF7ContentType(t *testing.T) {
	body := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
	p := startSpecProxy(t, "warn", "relaxed", chatOK())
	sid := newSession()
	_, _, _, session := postRaw(t, p, "/v1/chat/completions", sid, map[string]string{"Content-Type": "text/plain"}, body)
	warns, _ := normalized(lastCall(t, session))["parser_warnings"].([]any)
	found := false
	for _, w := range warns {
		if strings.Contains(fmt.Sprint(w), "content-type") {
			found = true
		}
	}
	if !found {
		t.Fatalf("warnings=%v", warns)
	}
}

func TestG2UnknownResponseFields(t *testing.T) {
	up := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"c","object":"chat.completion","created":1,"model":"m","future":1,"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop","logprobs":null}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2,"future_tokens":9}}`))
	}
	p := startSpecProxy(t, "warn", "strict", up)
	sid := newSession()
	_, _, client, session := postRaw(t, p, "/v1/chat/completions", sid, nil, []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`))
	n := normalized(lastCall(t, session))
	for _, v := range asAnySlice(n["spec_violations"]) {
		m := v.(map[string]any)
		if m["side"] == "response" {
			t.Fatalf("response violation=%v", m)
		}
	}
	if !bytes.Contains(client, []byte(`"future"`)) {
		t.Fatal("client bytes changed")
	}
}

func TestG5TrailingResponse(t *testing.T) {
	up := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"c","model":"m","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]} trailing`))
	}
	p := startSpecProxy(t, "warn", "relaxed", up)
	sid := newSession()
	_, _, client, session := postRaw(t, p, "/v1/chat/completions", sid, nil, []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`))
	n := normalized(lastCall(t, session))
	if n["output_text"] != "ok" {
		t.Fatalf("output_text=%v", n["output_text"])
	}
	found := false
	for _, w := range asAnySlice(n["parser_warnings"]) {
		if strings.Contains(fmt.Sprint(w), "trailing_data_after_json") {
			found = true
		}
	}
	if !found {
		t.Fatalf("warnings=%v", n["parser_warnings"])
	}
	if !bytes.Contains(client, []byte("trailing")) {
		t.Fatal("client bytes changed")
	}
}

func TestE10Determinism(t *testing.T) {
	body := []byte(`{"model":"m","messages":[{"role":"user","content":"hi","foo":1}],"tools":[{"type":"function","function":{"name":"shirt","parameters":{"type":"object","properties":{"color":{"enumDescriptions":{"a":"b"}}}}}}]}`)
	p := startSpecProxy(t, "warn", "strict", chatOK())
	var prev string
	for i := 0; i < 20; i++ {
		sid := newSession()
		_, _, _, session := postRaw(t, p, "/v1/chat/completions", sid, nil, body)
		n := normalized(lastCall(t, session))
		raw, _ := json.Marshal(n["spec_violations"])
		raw2, _ := json.Marshal(n["ignored_fields"])
		got := string(raw) + string(raw2)
		if i > 0 && got != prev {
			t.Fatalf("nondeterministic\n%s\n%s", prev, got)
		}
		prev = got
	}
}

func TestH1RequestDecodeWarnings(t *testing.T) {
	for _, mode := range []string{"off", "warn", "enforce"} {
		p := startSpecProxy(t, mode, "relaxed", chatOK())
		sid := newSession()
		_, _, _, session := postRaw(t, p, "/v1/chat/completions", sid, nil, []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]} xyz`))
		warns := asAnySlice(normalized(lastCall(t, session))["parser_warnings"])
		count := 0
		for _, w := range warns {
			if strings.Contains(fmt.Sprint(w), "trailing_data_after_json") {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("mode=%s want exactly one trailing_data_after_json warning, got %d: %v", mode, count, warns)
		}
	}
}

func assertKeyword(t *testing.T, n map[string]any, keyword string) {
	t.Helper()
	vs := asAnySlice(n["spec_violations"])
	if keyword == "" {
		for _, v := range vs {
			m := v.(map[string]any)
			if m["side"] == "response" {
				continue
			}
			t.Fatalf("unexpected violation %v", m)
		}
		return
	}
	for _, v := range vs {
		m := v.(map[string]any)
		if m["keyword"] == keyword {
			return
		}
	}
	t.Fatalf("missing keyword %s in %v", keyword, vs)
}

func assertIgnoredKind(t *testing.T, n map[string]any, kind string) {
	t.Helper()
	ig := asAnySlice(n["ignored_fields"])
	if kind == "" {
		return
	}
	for _, v := range ig {
		m := v.(map[string]any)
		if m["kind"] == kind {
			return
		}
	}
	t.Fatalf("missing ignored kind %s in %v", kind, ig)
}

func asAnySlice(v any) []any {
	s, _ := v.([]any)
	return s
}

func TestA6OverlayStaleProof(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "scripts", "prove-a6.sh")
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("scripts/prove-a6.sh not present: %v", err)
	}
}

func TestI1NoCrossTalk(t *testing.T) {
	p := startSpecProxy(t, "warn", "strict", chatOK())
	clean := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
	bad := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"foo":1}`)
	var wg sync.WaitGroup
	errCh := make(chan string, 20)
	for i := 0; i < 10; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			sid := newSession()
			_, _, _, session, err := postRawErr(p, "/v1/chat/completions", sid, nil, clean)
			if err != nil {
				errCh <- err.Error()
				return
			}
			call, err := lastCallErr(session)
			if err != nil {
				errCh <- err.Error()
				return
			}
			if len(asAnySlice(normalized(call)["spec_violations"])) != 0 {
				errCh <- "clean got violations"
			}
		}()
		go func() {
			defer wg.Done()
			sid := newSession()
			_, _, _, session, err := postRawErr(p, "/v1/chat/completions", sid, nil, bad)
			if err != nil {
				errCh <- err.Error()
				return
			}
			call, err := lastCallErr(session)
			if err != nil {
				errCh <- err.Error()
				return
			}
			if len(asAnySlice(normalized(call)["spec_violations"])) == 0 {
				errCh <- "bad missing violations"
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for e := range errCh {
		t.Error(e)
	}
}
