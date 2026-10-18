package llmtestlab

import (
	"encoding/json"
	"errors"
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
		"required": []any{"answer"},
		"properties": map[string]any{
			"answer": map[string]any{"type": "string"},
		},
	})
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
