// SPDX-License-Identifier: AGPL-3.0-only

//go:build e2e

package llmentary_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestLiveSpec_UnknownTopLevel_Chat(t *testing.T) {
	runLiveRow(t, "L1_chat", "/chat/completions", func(model string) []byte {
		return []byte(`{"model":` + quoteJSON(model) + `,"messages":[{"role":"user","content":"Reply with ok."}],"max_tokens":16,"foo":1}`)
	})
}

func TestLiveSpec_UnknownTopLevel_Responses(t *testing.T) {
	runLiveRow(t, "L1_responses", "/responses", func(model string) []byte {
		return []byte(`{"model":` + quoteJSON(model) + `,"input":"Reply with ok.","max_output_tokens":16,"foo":1}`)
	})
}

func TestLiveSpec_UnknownInMessage_Chat(t *testing.T) {
	runLiveRow(t, "L2_chat", "/chat/completions", func(model string) []byte {
		return []byte(`{"model":` + quoteJSON(model) + `,"messages":[{"role":"user","content":"Reply with ok.","foo":1}],"max_tokens":16}`)
	})
}

func TestLiveSpec_UnknownInInputItem_Responses(t *testing.T) {
	runLiveRow(t, "L2_responses", "/responses", func(model string) []byte {
		return []byte(`{"model":` + quoteJSON(model) + `,"input":[{"role":"user","content":"Reply with ok.","foo":1}],"max_output_tokens":16}`)
	})
}

func TestLiveSpec_UnknownInFunctionTool_Chat(t *testing.T) {
	runLiveRow(t, "L3_chat", "/chat/completions", func(model string) []byte {
		return []byte(`{"model":` + quoteJSON(model) + `,"messages":[{"role":"user","content":"Reply with ok."}],"max_tokens":16,"tools":[{"type":"function","function":{"name":"echo","parameters":{"type":"object","properties":{}},"foo":1}}]}`)
	})
}

func TestLiveSpec_UnknownInFunctionTool_Responses(t *testing.T) {
	runLiveRow(t, "L3_responses", "/responses", func(model string) []byte {
		return []byte(`{"model":` + quoteJSON(model) + `,"input":"Reply with ok.","max_output_tokens":16,"tools":[{"type":"function","name":"echo","parameters":{"type":"object","properties":{}},"foo":1}]}`)
	})
}

func TestLiveSpec_FunctionToolWithoutStrict_Responses(t *testing.T) {
	runLiveRow(t, "L4_responses", "/responses", func(model string) []byte {
		return []byte(`{"model":` + quoteJSON(model) + `,"input":"Reply with ok.","max_output_tokens":16,"tools":[{"type":"function","name":"echo","parameters":{"type":"object","properties":{"a":{"type":"string"}}}}]}`)
	})
}

func TestLiveSpec_BadEnum_Chat(t *testing.T) {
	runLiveRow(t, "L5_chat", "/chat/completions", func(model string) []byte {
		return []byte(`{"model":` + quoteJSON(model) + `,"messages":[{"role":"user","content":"Reply with ok."}],"max_tokens":16,"tool_choice":"maybe"}`)
	})
}

func TestLiveSpec_BadEnum_Responses(t *testing.T) {
	runLiveRow(t, "L5_responses", "/responses", func(model string) []byte {
		return []byte(`{"model":` + quoteJSON(model) + `,"input":"Reply with ok.","max_output_tokens":16,"tool_choice":"maybe"}`)
	})
}

func TestLiveSpec_MissingRequired_Chat(t *testing.T) {
	runLiveRow(t, "L6_chat", "/chat/completions", func(model string) []byte {
		return []byte(`{"model":` + quoteJSON(model) + `}`)
	})
}

func TestLiveSpec_MissingRequired_Responses(t *testing.T) {
	runLiveRow(t, "L6_responses", "/responses", func(string) []byte {
		return []byte(`{"input":"Reply with ok."}`)
	})
}

func TestLiveSpec_WrongType_Chat(t *testing.T) {
	runLiveRow(t, "L7_chat", "/chat/completions", func(model string) []byte {
		return []byte(`{"model":` + quoteJSON(model) + `,"messages":[{"role":"user","content":"Reply with ok."}],"temperature":"0.7"}`)
	})
}

func TestLiveSpec_WrongType_Responses(t *testing.T) {
	runLiveRow(t, "L7_responses", "/responses", func(model string) []byte {
		return []byte(`{"model":` + quoteJSON(model) + `,"input":"Reply with ok.","temperature":"0.7"}`)
	})
}

func TestLiveSpec_TrailingGarbage_Chat(t *testing.T) {
	runLiveRow(t, "L8_chat", "/chat/completions", func(model string) []byte {
		return []byte(`{"model":` + quoteJSON(model) + `,"messages":[{"role":"user","content":"Reply with ok."}],"max_tokens":16} xyz`)
	})
}

func TestLiveSpec_TrailingGarbage_Responses(t *testing.T) {
	runLiveRow(t, "L8_responses", "/responses", func(model string) []byte {
		return []byte(`{"model":` + quoteJSON(model) + `,"input":"Reply with ok.","max_output_tokens":16} xyz`)
	})
}

func TestLiveSpec_InvalidUTF8_Chat(t *testing.T) {
	runLiveProbe(t, "invalid_utf8_chat", "/chat/completions", func(model string) []byte {
		return []byte(`{"model":` + quoteJSON(model) + `,"messages":[{"role":"user","content":"hi` + "\xff\xfe" + `"}],"max_tokens":16}`)
	})
}

func TestLiveSpec_InvalidUTF8_Responses(t *testing.T) {
	runLiveProbe(t, "invalid_utf8_responses", "/responses", func(model string) []byte {
		return []byte(`{"model":` + quoteJSON(model) + `,"input":"hi` + "\xff\xfe" + `","max_output_tokens":16}`)
	})
}

func TestLiveSpec_UTF8BOM_Chat(t *testing.T) {
	runLiveProbe(t, "utf8_bom_chat", "/chat/completions", func(model string) []byte {
		return append([]byte{0xEF, 0xBB, 0xBF}, []byte(`{"model":`+quoteJSON(model)+`,"messages":[{"role":"user","content":"Reply with ok."}],"max_tokens":16}`)...)
	})
}

func TestLiveSpec_DuplicateKeys_Chat(t *testing.T) {
	runLiveProbe(t, "duplicate_keys_chat", "/chat/completions", func(model string) []byte {
		return []byte(`{"model":` + quoteJSON(model) + `,"model":` + quoteJSON(model) + `,"messages":[{"role":"user","content":"Reply with ok."}],"max_tokens":16}`)
	})
}

func TestLiveSpec_UnknownInContentPart_Chat(t *testing.T) {
	runLiveProbe(t, "content_part_chat", "/chat/completions", func(model string) []byte {
		return []byte(`{"model":` + quoteJSON(model) + `,"messages":[{"role":"user","content":[{"type":"text","text":"Reply with ok.","foo":1}]}],"max_tokens":16}`)
	})
}

func TestLiveSpec_UnknownInToolCall_Chat(t *testing.T) {
	runLiveProbe(t, "tool_call_chat", "/chat/completions", func(model string) []byte {
		return []byte(`{"model":` + quoteJSON(model) + `,"messages":[{"role":"user","content":"Call echo."},{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"echo","arguments":"{}"},"foo":1}]},{"role":"tool","tool_call_id":"call_1","content":"ok"}],"max_tokens":16}`)
	})
}

func TestLiveSpec_UnknownInContentPart_Responses(t *testing.T) {
	runLiveProbe(t, "content_part_responses", "/responses", func(model string) []byte {
		return []byte(`{"model":` + quoteJSON(model) + `,"input":[{"role":"user","content":[{"type":"input_text","text":"Reply with ok.","foo":1}]}],"max_output_tokens":16}`)
	})
}

func TestLiveEnforce_RejectsWithoutUpstream(t *testing.T) {
	apiKey := requireAPIKey(t)
	p := startLiveProxy(t, "enforce", "relaxed")
	sid := liveSession()
	status, hdr, body, session := postLive(t, apiKey, p, "/chat/completions", sid, []byte(`{}`))
	if status != 400 {
		t.Fatalf("status=%d body=%s", status, redactLiveBody(body))
	}
	if hdr.Get("X-Llmentary-Rejected") == "" {
		t.Fatal("missing X-Llmentary-Rejected")
	}
	if hdr.Get("x-request-id") != "" || hdr.Get("X-Request-Id") != "" {
		t.Fatal("must not expose an OpenAI x-request-id on a local reject")
	}
	call := liveLastCall(t, session)
	if call["upstream_called"] != false {
		t.Fatalf("upstream_called=%v", call["upstream_called"])
	}
	errObj, _ := liveNormalized(call)["error"].(map[string]any)
	if errObj["source"] != "proxy" {
		t.Fatalf("C5 normalized.error.source=%v want proxy", errObj["source"])
	}
	p2 := startLiveProxy(t, "enforce", "relaxed")
	sid2 := liveSession()
	okBody := []byte(`{"model":` + quoteJSON(liveModel()) + `,"messages":[{"role":"user","content":"Reply with ok."}],"max_tokens":16}`)
	status2, _, body2, _ := postLive(t, apiKey, p2, "/chat/completions", sid2, okBody)
	if status2 != 200 {
		t.Fatalf("valid enforce request: status=%d body=%s", status2, redactLiveBody(body2))
	}
}

func TestLiveIgnored_EnumDescriptions_ChatTool(t *testing.T) {
	runEnumDescriptionsLive(t, "/chat/completions", chatEnumDescBody(false), false, "chat.tools.function.parameters")
}

func TestLiveIgnored_EnumDescriptions_ChatToolStrict(t *testing.T) {
	runEnumDescriptionsLive(t, "/chat/completions", chatEnumDescBody(true), true, "chat.tools.function.parameters")
}

func TestLiveIgnored_EnumDescriptions_ResponsesTool(t *testing.T) {
	runEnumDescriptionsLive(t, "/responses", responsesEnumDescBody(boolPtr(false)), false, "responses.tools.parameters")
}

func TestLiveIgnored_EnumDescriptions_ResponsesToolStrictOmitted(t *testing.T) {
	apiKey := requireAPIKey(t)
	p := startLiveProxy(t, "warn", "relaxed")
	sid := liveSession()
	status, _, body, session := postLive(t, apiKey, p, "/responses", sid, responsesEnumDescBody(nil)(liveModel()))
	if status != 200 {
		t.Fatalf("status=%d body=%s", status, redactLiveBody(body))
	}
	n := liveNormalized(liveLastCall(t, session))
	assertLiveEnumDescriptions(t, "responses.tools.parameters", n, false)
	var env map[string]any
	_ = json.Unmarshal(body, &env)
	if tools, ok := env["tools"].([]any); ok && len(tools) > 0 {
		tool, _ := tools[0].(map[string]any)
		if b, ok := tool["strict"].(bool); ok && !b {
			confirmed := false
			for _, v := range liveSlice(n["ignored_fields"]) {
				m := v.(map[string]any)
				if m["kind"] == "strict_fallback" && m["confirmed"] == true {
					confirmed = true
				}
			}
			if !confirmed {
				t.Fatalf("expected confirmed strict_fallback: %v", n["ignored_fields"])
			}
		}
	}
}

func TestLiveIgnored_EnumDescriptions_ChatResponseFormat(t *testing.T) {
	model := liveModel()
	body := []byte(`{"model":` + quoteJSON(model) + `,"messages":[{"role":"user","content":"Reply with ok."}],"max_tokens":16,"response_format":{"type":"json_schema","json_schema":{"name":"color","strict":true,"schema":{"type":"object","properties":{"color":{"type":"string","enum":["red"],"enumDescriptions":{"red":"warm"}}},"required":["color"],"additionalProperties":false}}}}`)
	runEnumDescriptionsLive(t, "/chat/completions", func(string) []byte { return body }, true, "chat.response_format.json_schema")
}

func TestLiveIgnored_EnumDescriptions_ResponsesTextFormat(t *testing.T) {
	model := liveModel()
	body := []byte(`{"model":` + quoteJSON(model) + `,"input":"Reply with ok.","max_output_tokens":16,"text":{"format":{"type":"json_schema","name":"color","strict":true,"schema":{"type":"object","properties":{"color":{"type":"string","enum":["red"],"enumDescriptions":{"red":"warm"}}},"required":["color"],"additionalProperties":false}}}}`)
	runEnumDescriptionsLive(t, "/responses", func(string) []byte { return body }, true, "responses.text.format")
}

func runLiveRow(t *testing.T, rowID, path string, body func(model string) []byte) {
	t.Helper()
	apiKey := requireAPIKey(t)
	beh := loadLiveBehavior(t)
	row, ok := beh.Rows[rowID]
	if !ok {
		t.Fatalf("missing live-behavior row %s", rowID)
	}
	raw := body(liveModel())
	relaxedP := startLiveProxy(t, "warn", "relaxed")
	sid := liveSession()
	status, _, resp, session := postLive(t, apiKey, relaxedP, path, sid, raw)
	assertLiveError(t, row, status, resp)
	relaxedN := liveNormalized(liveLastCall(t, session))
	strictP := startLiveProxy(t, "warn", "strict")
	sid2 := liveSession()
	_, _, _, session2 := postLive(t, apiKey, strictP, path, sid2, raw)
	strictN := liveNormalized(liveLastCall(t, session2))
	assertPrediction(t, row, relaxedN, strictN, status)
	if row.IgnoredKind != "" {
		found := false
		for _, v := range liveSlice(relaxedN["ignored_fields"]) {
			if v.(map[string]any)["kind"] == row.IgnoredKind {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected ignored_fields kind %s: %v", row.IgnoredKind, relaxedN["ignored_fields"])
		}
		if len(requestSideViolations(relaxedN)) != 0 {
			t.Fatalf("C3: relaxed spec_violations should be empty: %v", relaxedN["spec_violations"])
		}
	}
	if row.IgnoredFields != nil && !*row.IgnoredFields {
		if len(liveSlice(relaxedN["ignored_fields"])) != 0 {
			t.Fatalf("did not expect ignored_fields: %v", relaxedN["ignored_fields"])
		}
	}
}

func runLiveProbe(t *testing.T, rowID, path string, body func(model string) []byte) {
	runLiveRow(t, rowID, path, body)
}

func runEnumDescriptionsLive(t *testing.T, path string, bodyFn func(model string) []byte, effectiveStrict bool, context string) {
	t.Helper()
	apiKey := requireAPIKey(t)
	p := startLiveProxy(t, "warn", "relaxed")
	sid := liveSession()
	status, _, resp, session := postLive(t, apiKey, p, path, sid, bodyFn(liveModel()))
	if status != 200 {
		t.Fatalf("status=%d body=%s", status, redactLiveBody(resp))
	}
	n := liveNormalized(liveLastCall(t, session))
	assertLiveEnumDescriptions(t, context, n, effectiveStrict)
}

func assertLiveEnumDescriptions(t *testing.T, context string, n map[string]any, effectiveStrict bool) {
	t.Helper()
	want := map[string]any{
		"key": "enumDescriptions", "kind": "unknown_schema_keyword",
		"context": context, "upstream_behavior": "silently_ignored",
		"effective_strict":      effectiveStrict,
		"hint":                  "OpenAI does not read `enumDescriptions`; the model never sees it. Move the per-value descriptions into this property's `description`.",
		"suggested_description": "red: warm",
	}
	switch context {
	case "chat.tools.function.parameters":
		want["path"] = "tools[0].function.parameters.properties.color.enumDescriptions"
		want["pointer"] = "/tools/0/function/parameters/properties/color/enumDescriptions"
	case "responses.tools.parameters":
		want["path"] = "tools[0].parameters.properties.color.enumDescriptions"
		want["pointer"] = "/tools/0/parameters/properties/color/enumDescriptions"
	case "chat.response_format.json_schema":
		want["path"] = "response_format.json_schema.schema.properties.color.enumDescriptions"
		want["pointer"] = "/response_format/json_schema/schema/properties/color/enumDescriptions"
	case "responses.text.format":
		want["path"] = "text.format.schema.properties.color.enumDescriptions"
		want["pointer"] = "/text/format/schema/properties/color/enumDescriptions"
	}
	assertIgnoredExact(t, n, want)
}

func chatEnumDescBody(strict bool) func(string) []byte {
	return func(model string) []byte {
		strictJSON := ""
		if strict {
			strictJSON = `,"strict":true`
		}
		return []byte(`{"model":` + quoteJSON(model) + `,"messages":[{"role":"user","content":"Reply with ok."}],"max_tokens":16,"tools":[{"type":"function","function":{"name":"shirt","parameters":{"type":"object","properties":{"color":{"type":"string","enum":["red"],"enumDescriptions":{"red":"warm"}}},"required":["color"],"additionalProperties":false}` + strictJSON + `}}]}`)
	}
}

func responsesEnumDescBody(strict *bool) func(string) []byte {
	return func(model string) []byte {
		strictJSON := ""
		if strict != nil {
			strictJSON = fmt.Sprintf(`,"strict":%v`, *strict)
		}
		return []byte(`{"model":` + quoteJSON(model) + `,"input":"Reply with ok.","max_output_tokens":16,"tools":[{"type":"function","name":"shirt"` + strictJSON + `,"parameters":{"type":"object","properties":{"color":{"type":"string","enum":["red"],"enumDescriptions":{"red":"warm"}}},"required":["color"],"additionalProperties":false}}]}`)
	}
}

func boolPtr(v bool) *bool { return &v }

func TestLiveCostGuardPresent(t *testing.T) {
	_ = requireAPIKey(t)
	if liveRequestCap != 125 {
		t.Fatalf("cap=%d", liveRequestCap)
	}
	if msg := checkLiveCost(int64(liveRequestCap)+1, int64(liveRequestCap)); msg == "" {
		t.Fatal("cost guard must trip when upstream calls exceed the cap")
	}
	if msg := checkLiveCost(int64(liveRequestCap), int64(liveRequestCap)); msg != "" {
		t.Fatalf("cost guard must allow calls at the cap: %s", msg)
	}
	if msg := checkLiveCost(0, int64(liveRequestCap)); msg != "" {
		t.Fatalf("cost guard must allow zero calls: %s", msg)
	}
}

func TestLiveBehaviorPinsStreamError(t *testing.T) {
	beh := loadLiveBehavior(t)
	row := beh.Rows["stream_error_responses"]
	if row.Test != "TestE2EResponsesStreamErrorShape" || row.Status == 0 {
		t.Fatalf("stream error pin missing: %+v", row)
	}
}

func redact(_ *testing.T, body []byte) string { return string(bytes.TrimSpace(body)) }

func TestLiveSpecJSONHasNoSecrets(t *testing.T) {
	raw := liveBehaviorRaw(t)
	if strings.Contains(strings.ToLower(string(raw)), "sk-") {
		t.Fatal("live-behavior.json must not contain secrets")
	}
}
