// SPDX-License-Identifier: AGPL-3.0-only

package proxy_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/barzoj-co/llm-test-lab/internal/lab"
)

func assertCallCount(t testing.TB, calls []lab.Call, want int) {
	t.Helper()
	if got := len(calls); got != want {
		t.Fatalf("call count mismatch: got=%d want=%d", got, want)
	}
}

func assertRetryCount(t testing.TB, calls []lab.Call, want int) {
	t.Helper()
	got := 0
	for _, call := range calls {
		if call.RetryCount != nil && *call.RetryCount > 0 {
			got++
		}
	}
	if got != want {
		t.Fatalf("retry call count mismatch: got %d, want %d", got, want)
	}
}

func assertToolCalled(t testing.TB, calls []lab.Call, name string, wantArgs any) {
	t.Helper()
	for _, call := range calls {
		for _, toolCall := range call.Normalized.ToolCalls {
			if toolCall.Name != name {
				continue
			}
			if wantArgs == nil || jsonMatches(t, toolCall.Arguments, wantArgs) {
				return
			}
		}
	}
	t.Fatalf("expected tool %q to be called with args %s; calls=%d", name, mustJSONString(wantArgs), len(calls))
}

func assertToolCalledSubset(t testing.TB, calls []lab.Call, name string, want map[string]any) {
	t.Helper()
	for _, call := range calls {
		for _, tool := range call.Normalized.ToolCalls {
			if tool.Name != name {
				continue
			}
			var got any
			if len(tool.Arguments) == 0 {
				got = map[string]any{}
			} else if err := json.Unmarshal(tool.Arguments, &got); err != nil {
				continue
			}
			if jsonSubset(got, want) {
				return
			}
		}
	}
	t.Fatalf("expected tool %q to be called", name)
}

func assertToolCalledArgPath(t testing.TB, calls []lab.Call, name, path string, want any) {
	t.Helper()
	for _, call := range calls {
		for _, tool := range call.Normalized.ToolCalls {
			if tool.Name != name {
				continue
			}
			var got any
			if err := json.Unmarshal(tool.Arguments, &got); err != nil {
				continue
			}
			value, ok := jsonPath(got, path)
			if ok && jsonDeepEqual(value, want) {
				return
			}
		}
	}
	t.Fatalf("expected tool %q to be called", name)
}

func assertToolNotCalled(t testing.TB, calls []lab.Call, name string) {
	t.Helper()
	for _, call := range calls {
		for _, toolCall := range call.Normalized.ToolCalls {
			if toolCall.Name == name {
				t.Fatalf("expected tool %q not to be called; got args=%s in call=%s", name, string(toolCall.Arguments), call.ID)
			}
		}
	}
}

func assertToolOrder(t testing.TB, calls []lab.Call, names ...string) {
	t.Helper()
	got := make([]string, 0)
	for _, call := range calls {
		for _, toolCall := range call.Normalized.ToolCalls {
			got = append(got, toolCall.Name)
		}
	}
	if !reflect.DeepEqual(got, names) {
		t.Fatalf("tool order mismatch: got=%v want=%v", got, names)
	}
}

func assertFinishReason(t testing.TB, calls []lab.Call, reason string) {
	t.Helper()
	for _, call := range calls {
		if call.Normalized.FinishReason == reason || call.Normalized.NativeFinishReason == reason {
			return
		}
	}
	t.Fatalf("expected finish reason %q", reason)
}

func assertTokensBelow(t testing.TB, calls []lab.Call, maxTotal int) {
	t.Helper()
	assertTokenBound(t, calls, maxTotal, true)
}

func assertTokensAtMost(t testing.TB, calls []lab.Call, maxTotal int) {
	t.Helper()
	assertTokenBound(t, calls, maxTotal, false)
}

func assertTokenBound(t testing.TB, calls []lab.Call, maxTotal int, strict bool) {
	t.Helper()
	total, known := 0, false
	for _, call := range calls {
		if call.Normalized.Usage == nil {
			continue
		}
		if call.Normalized.Usage.TotalTokens != nil {
			total += *call.Normalized.Usage.TotalTokens
			known = true
		} else {
			if call.Normalized.Usage.InputTokens != nil {
				total += *call.Normalized.Usage.InputTokens
				known = true
			}
			if call.Normalized.Usage.OutputTokens != nil {
				total += *call.Normalized.Usage.OutputTokens
				known = true
			}
		}
	}
	if !known {
		t.Fatalf("no token usage was reported")
	}
	if strict && total >= maxTotal {
		t.Fatalf("token total too high: got=%d want<%d", total, maxTotal)
	}
	if !strict && total > maxTotal {
		t.Fatalf("token total too high: got %d, want <=%d", total, maxTotal)
	}
}

func assertNoErrors(t testing.TB, calls []lab.Call) {
	t.Helper()
	for _, call := range calls {
		if call.Normalized.Error != nil {
			t.Fatalf("unexpected error in call #%d: %+v", call.Seq, call.Normalized.Error)
		}
	}
}

func assertHasError(t testing.TB, calls []lab.Call) {
	t.Helper()
	for _, call := range calls {
		if call.Normalized.Error != nil {
			return
		}
	}
	t.Fatalf("expected at least one LLM error")
}

func assertMidStreamError(t testing.TB, calls []lab.Call) {
	t.Helper()
	for _, call := range calls {
		if call.Normalized.Error != nil && call.Normalized.Error.MidStream {
			return
		}
	}
	t.Fatalf("expected an error inside an HTTP-200 stream")
}

func assertResponseJSONValid(t testing.TB, calls []lab.Call) {
	t.Helper()
	for _, call := range calls {
		if json.Valid([]byte(call.Normalized.OutputText)) {
			return
		}
	}
	t.Fatalf("expected at least one response to contain valid JSON")
}

func assertResponseJSONSchemaValid(t testing.TB, calls []lab.Call, schema map[string]any) {
	t.Helper()
	for _, call := range calls {
		var value any
		if json.Unmarshal([]byte(call.Normalized.OutputText), &value) == nil && validateBasicSchema(value, schema) == nil {
			return
		}
	}
	t.Fatalf("expected at least one response to satisfy schema %s", mustJSONString(schema))
}

func jsonMatches(t testing.TB, raw json.RawMessage, want any) bool {
	t.Helper()
	if want == nil {
		return true
	}
	wantBytes, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal wanted args: %v", err)
	}
	var gotValue any
	if len(raw) == 0 {
		gotValue = map[string]any{}
	} else if err := json.Unmarshal(raw, &gotValue); err != nil {
		return false
	}
	var wantValue any
	if err := json.Unmarshal(wantBytes, &wantValue); err != nil {
		t.Fatalf("unmarshal wanted args: %v", err)
	}
	return reflect.DeepEqual(gotValue, wantValue)
}

func jsonSubset(got any, want any) bool {
	switch wantTyped := want.(type) {
	case map[string]any:
		gotMap, ok := got.(map[string]any)
		if !ok {
			return false
		}
		for key, wantValue := range wantTyped {
			if !jsonSubset(gotMap[key], wantValue) {
				return false
			}
		}
		return true
	case []any:
		gotSlice, ok := got.([]any)
		if !ok || len(gotSlice) < len(wantTyped) {
			return false
		}
		for i, wantValue := range wantTyped {
			if !jsonSubset(gotSlice[i], wantValue) {
				return false
			}
		}
		return true
	default:
		return jsonDeepEqual(got, want)
	}
}

func jsonPath(value any, path string) (any, bool) {
	current := value
	for _, part := range strings.Split(path, ".") {
		if part == "" || part == "$" {
			continue
		}
		switch typed := current.(type) {
		case map[string]any:
			var ok bool
			current, ok = typed[part]
			if !ok {
				return nil, false
			}
		case []any:
			var idx int
			if _, err := fmt.Sscanf(part, "%d", &idx); err != nil || idx < 0 || idx >= len(typed) {
				return nil, false
			}
			current = typed[idx]
		default:
			return nil, false
		}
	}
	return current, true
}

func jsonDeepEqual(got, want any) bool {
	gotBytes, _ := json.Marshal(got)
	wantBytes, _ := json.Marshal(want)
	var gotJSON any
	var wantJSON any
	_ = json.Unmarshal(gotBytes, &gotJSON)
	_ = json.Unmarshal(wantBytes, &wantJSON)
	return fmt.Sprintf("%#v", gotJSON) == fmt.Sprintf("%#v", wantJSON)
}

func validateBasicSchema(value any, schema map[string]any) error {
	if typ, _ := schema["type"].(string); typ != "" {
		if !matchesJSONType(value, typ) {
			return fmt.Errorf("type mismatch: got %T want %s", value, typ)
		}
	}
	if required := requiredKeys(schema["required"]); len(required) > 0 {
		obj, _ := value.(map[string]any)
		for _, key := range required {
			if _, ok := obj[key]; !ok {
				return fmt.Errorf("missing required property %q", key)
			}
		}
	}
	if props, ok := schema["properties"].(map[string]any); ok {
		obj, _ := value.(map[string]any)
		for key, rawSchema := range props {
			if child, ok := obj[key]; ok {
				childSchema, _ := rawSchema.(map[string]any)
				if childSchema != nil {
					if err := validateBasicSchema(child, childSchema); err != nil {
						return fmt.Errorf("%s: %w", key, err)
					}
				}
			}
		}
	}
	return nil
}

func requiredKeys(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if key, ok := item.(string); ok {
				out = append(out, key)
			}
		}
		return out
	default:
		return nil
	}
}

func matchesJSONType(value any, typ string) bool {
	switch typ {
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "number":
		_, ok := value.(float64)
		return ok
	case "integer":
		n, ok := value.(float64)
		return ok && n == float64(int64(n))
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "null":
		return value == nil
	default:
		return true
	}
}

func mustJSONString(v any) string {
	if v == nil {
		return "<any>"
	}
	var b bytes.Buffer
	if err := json.NewEncoder(&b).Encode(v); err != nil {
		return fmt.Sprintf("%v", v)
	}
	return strings.TrimSpace(b.String())
}
