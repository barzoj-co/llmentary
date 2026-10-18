// SPDX-License-Identifier: AGPL-3.0-only

//go:build e2e

package llmtestlab_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/barzoj-co/llm-test-lab/internal/lab"
)

func assertCallCount(t testing.TB, calls []lab.Call, want int) {
	t.Helper()
	if got := len(calls); got != want {
		t.Fatalf("LLM call count mismatch: got %d, want %d", got, want)
	}
}

func assertNoRetries(t testing.TB, calls []lab.Call) {
	t.Helper()
	got := 0
	for _, call := range calls {
		if call.RetryCount != nil && *call.RetryCount > 0 {
			got++
		}
	}
	if got != 0 {
		t.Fatalf("retry call count mismatch: got %d, want %d", got, 0)
	}
}

func assertToolCalled(t testing.TB, calls []lab.Call, name string) {
	t.Helper()
	assertToolCalledSubset(t, calls, name, nil)
}

func assertToolCalledSubset(t testing.TB, calls []lab.Call, name string, want map[string]any) {
	t.Helper()
	for _, call := range calls {
		for _, tool := range call.Normalized.ToolCalls {
			if tool.Name != name {
				continue
			}
			if want == nil {
				return
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

func assertToolResultFedBack(t testing.TB, calls []lab.Call, callID string) {
	t.Helper()
	for _, call := range calls {
		for _, result := range call.Normalized.ToolResultsIn {
			if result.CallID == callID {
				return
			}
		}
	}
	t.Fatalf("expected tool result for call id %q to be fed back", callID)
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

func assertResponseContains(t testing.TB, calls []lab.Call, substr string) {
	t.Helper()
	for _, call := range calls {
		if strings.Contains(call.Normalized.OutputText, substr) {
			return
		}
	}
	t.Fatalf("expected response content to contain %q", substr)
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

func assertTokensAtMost(t testing.TB, calls []lab.Call, maxTotal int) {
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
		t.Fatalf("no token usage reported")
	}
	if total > maxTotal {
		t.Fatalf("token total too high: got %d, want <=%d", total, maxTotal)
	}
}

func assertLatencyBelow(t testing.TB, calls []lab.Call, d time.Duration) {
	t.Helper()
	for _, call := range calls {
		if time.Duration(call.LatencyMS)*time.Millisecond >= d {
			t.Fatalf("call #%d latency %s exceeds %s", call.Seq, time.Duration(call.LatencyMS)*time.Millisecond, d)
		}
	}
}

func assertTTFTBelow(t testing.TB, calls []lab.Call, d time.Duration) {
	t.Helper()
	for _, call := range calls {
		if call.FirstByteAt == nil {
			continue
		}
		ttft := call.FirstByteAt.Sub(call.StartedAt)
		if ttft >= d {
			t.Fatalf("call #%d TTFT %s exceeds %s", call.Seq, ttft, d)
		}
	}
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
