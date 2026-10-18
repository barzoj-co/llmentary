// SPDX-License-Identifier: MIT

package llmtestlab

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestAssertionsToolAndMetadata(t *testing.T) {
	trace := fixtureTrace()
	a := &Assertions{t: t, trace: trace, fatal: true}
	a.CallCount(2)
	a.ToolCalled("get_weather", ArgsSubset(map[string]any{"city": "Paris"}), ArgPath("unit", "celsius"))
	a.ToolCalledTimes("get_weather", 1)
	a.ToolNotCalled("delete_user")
	a.ToolOrder("get_weather", "summarize")
	a.ToolBefore("get_weather", "summarize")
	a.ParallelToolCalls("get_weather", "summarize")
	a.ToolResultFedBack("call_weather")
	a.Model("gpt-test")
	a.RequestParam("temperature", 0)
	a.FinishReason("tool_calls")
	a.TokensBelow(100)
	a.LatencyBelow(2 * time.Second)
	a.TTFTBelow(time.Second)
	a.NoErrors()
	a.NoRetries()
	a.SystemPromptContains("weather bot")
	a.ResponseContains("sunny")
	a.ResponseMatches(`sun+`)
}

func TestArgsMatchCustom(t *testing.T) {
	trace := fixtureTrace()
	a := &Assertions{t: t, trace: trace, fatal: true}
	a.ToolCalled("get_weather", ArgsMatch(func(raw json.RawMessage) error {
		var args map[string]string
		if err := json.Unmarshal(raw, &args); err != nil {
			return err
		}
		if args["city"] != "Paris" {
			return errors.New("wrong city")
		}
		return nil
	}))
}

func TestQueryFilters(t *testing.T) {
	q := fixtureTrace().Query().Upstream("openai").APIType("chat").Model("gpt-test").Status(200)
	if q.Len() != 2 {
		t.Fatalf("filtered calls = %d, want 2", q.Len())
	}
	if q.Index(1).Calls()[0].Seq != 2 {
		t.Fatalf("index did not preserve call order")
	}
}

func TestResponseJSONSchemaValid(t *testing.T) {
	trace := Trace{SessionID: "s", Calls: []Call{{
		Seq:        1,
		Normalized: Normalized{OutputText: `{"answer":"ok"}`},
	}}}
	a := &Assertions{t: t, trace: trace, fatal: true}
	a.ResponseJSONValid()
	a.ResponseJSONSchemaValid(map[string]any{
		"type":     "object",
		"required": []string{"answer"},
		"properties": map[string]any{
			"answer": map[string]any{"type": "string"},
		},
	})
}

func TestJSONPathMissingKeyDoesNotMatchNull(t *testing.T) {
	var value any
	if err := json.Unmarshal([]byte(`{"present":null}`), &value); err != nil {
		t.Fatal(err)
	}
	if got, ok := jsonPath(value, "missing"); ok || got != nil {
		t.Fatalf("missing path should not match nil")
	}
}

func TestSchemaIntegerType(t *testing.T) {
	if err := validateBasicSchema(map[string]any{"n": "not-int"}, map[string]any{
		"type":     "object",
		"required": []string{"n"},
		"properties": map[string]any{
			"n": map[string]any{"type": "integer"},
		},
	}); err == nil {
		t.Fatalf("string should not satisfy integer schema")
	}
	if err := validateBasicSchema(map[string]any{"n": float64(3)}, map[string]any{
		"type":     "object",
		"required": []string{"n"},
		"properties": map[string]any{
			"n": map[string]any{"type": "integer"},
		},
	}); err != nil {
		t.Fatalf("integer-valued number should satisfy schema: %v", err)
	}
}

func TestAssertionFailureMessagesIncludeTraceSummary(t *testing.T) {
	trace := fixtureTrace()
	trace.Calls = append(trace.Calls, Call{
		ID:         "call_error",
		SessionID:  trace.SessionID,
		Seq:        3,
		Provider:   "openai",
		APIFamily:  "openai.chat",
		HTTPStatus: 200,
		Normalized: Normalized{
			FinishReason: "error",
			Error: &CallError{
				Type:      "rate_limit_error",
				Code:      "rate_limit_exceeded",
				Message:   "slow down",
				MidStream: true,
			},
		},
	})

	nonFatal := &fakeTB{}
	check := &Assertions{t: nonFatal, trace: trace}
	check.ToolCalled("missing_tool")
	check.FinishReason("length")
	trace.Query().APIType("responses").Check(nonFatal).CallCount(1)

	joined := strings.Join(nonFatal.messages, "\n")
	for _, want := range []string{
		`expected tool "missing_tool" to be called`,
		`expected finish reason "length"`,
		`LLM call count mismatch`,
		`Trace summary:`,
		trace.SessionID,
		`get_weather args={"city":"Paris","unit":"celsius"}`,
		`finish=tool_calls`,
		`error type=rate_limit_error code=rate_limit_exceeded message=slow down mid_stream=true`,
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("failure output missing %q:\n%s", want, joined)
		}
	}
	if !nonFatal.failed || nonFatal.fatal {
		t.Fatalf("non-fatal assertions should record errors without fatal: %+v", nonFatal)
	}

	fatal := &fakeTB{}
	require := &Assertions{t: fatal, trace: trace, fatal: true}
	require.ToolCalled("missing_tool")
	if !fatal.fatal || !fatal.failed {
		t.Fatalf("fatal assertion did not record fatal failure: %+v", fatal)
	}
	if got := strings.Join(fatal.messages, "\n"); !strings.Contains(got, `expected tool "missing_tool" to be called`) || !strings.Contains(got, "Trace summary:") {
		t.Fatalf("fatal output missing assertion/summary:\n%s", got)
	}
}

type fakeTB struct {
	testing.TB
	messages []string
	failed   bool
	fatal    bool
}

func (f *fakeTB) Helper() {}

func (f *fakeTB) Error(args ...any) {
	f.failed = true
	f.messages = append(f.messages, fmt.Sprint(args...))
}

func (f *fakeTB) Errorf(format string, args ...any) {
	f.failed = true
	f.messages = append(f.messages, fmt.Sprintf(format, args...))
}

func (f *fakeTB) Fatal(args ...any) {
	f.failed = true
	f.fatal = true
	f.messages = append(f.messages, fmt.Sprint(args...))
}

func (f *fakeTB) Fatalf(format string, args ...any) {
	f.failed = true
	f.fatal = true
	f.messages = append(f.messages, fmt.Sprintf(format, args...))
}

func fixtureTrace() Trace {
	firstByte := time.Now().Add(50 * time.Millisecond)
	retry := 0
	return Trace{SessionID: "test-session", Calls: []Call{
		{
			ID:          "call_1",
			SessionID:   "test-session",
			Seq:         1,
			StartedAt:   time.Now(),
			FirstByteAt: &firstByte,
			LatencyMS:   100,
			Provider:    "openai",
			APIFamily:   "openai.chat",
			HTTPStatus:  200,
			RetryCount:  &retry,
			RawRequest:  []byte(`{"model":"gpt-test","temperature":0}`),
			Normalized: Normalized{
				Model:        Model{Requested: "gpt-test", Returned: "gpt-test"},
				SystemPrompt: "weather bot",
				FinishReason: "tool_calls",
				OutputText:   "sunny",
				Usage:        &Usage{TotalTokens: intPtrForTest(42)},
				ToolCalls: []ToolCall{
					{Index: 0, ID: "call_weather", Name: "get_weather", ArgumentsRaw: `{"city":"Paris","unit":"celsius"}`, Arguments: json.RawMessage(`{"city":"Paris","unit":"celsius"}`)},
					{Index: 1, ID: "call_summary", Name: "summarize", ArgumentsRaw: `{}`, Arguments: json.RawMessage(`{}`)},
				},
			},
		},
		{
			ID:         "call_2",
			SessionID:  "test-session",
			Seq:        2,
			Provider:   "openai",
			APIFamily:  "openai.chat",
			HTTPStatus: 200,
			Normalized: Normalized{
				Model:         Model{Requested: "gpt-test"},
				FinishReason:  "stop",
				ToolResultsIn: []ToolResult{{CallID: "call_weather"}},
			},
		},
	}}
}

func intPtrForTest(v int) *int { return &v }
