// SPDX-License-Identifier: AGPL-3.0-only

package tracetest

import (
	"strings"
	"testing"

	"github.com/barzoj-co/llmentary/internal/proxy"
)

func TestTraceSummaryAndQuery(t *testing.T) {
	retry := 2
	tr := Trace{
		SessionID: "s1",
		Calls: []proxy.Call{
			{
				Seq:        1,
				Provider:   "openai",
				APIFamily:  "openai.chat",
				HTTPStatus: 200,
				LatencyMS:  12,
				Normalized: proxy.Normalized{
					FinishReason: "stop",
					Model:        proxy.Model{Requested: "gpt-4.1", Returned: "gpt-4.1-mini"},
					ToolCalls:    []proxy.ToolCall{{Index: 0, Name: "lookup", ArgumentsRaw: `{"q":"hi"}`}},
				},
			},
			{
				Seq:        2,
				Provider:   "openai",
				APIFamily:  "openai.responses",
				HTTPStatus: 0,
				RetryCount: &retry,
				Normalized: proxy.Normalized{
					FinishReason: "cancelled",
					Error:        &proxy.CallError{Type: "client_aborted", Code: "aborted", Message: "gone", MidStream: false},
				},
			},
		},
	}
	sum := tr.Summary()
	for _, want := range []string{
		"session s1: 2 call(s)",
		"#1 openai openai.chat status=200",
		"retry=unknown",
		"tool[0] lookup args={\"q\":\"hi\"}",
		"#2 openai openai.responses status=0",
		"retry=2",
		"error type=client_aborted",
	} {
		if !strings.Contains(sum, want) {
			t.Fatalf("summary missing %q:\n%s", want, sum)
		}
	}

	q := tr.Query()
	if len(q.Calls()) != 2 {
		t.Fatalf("calls=%d", len(q.Calls()))
	}
	chat := q.APIType("chat")
	if len(chat.Calls()) != 1 || chat.Calls()[0].Seq != 1 {
		t.Fatalf("chat filter=%+v", chat.Calls())
	}
	resp := q.APIType("openai.responses")
	if len(resp.Calls()) != 1 || resp.Calls()[0].Seq != 2 {
		t.Fatalf("responses filter=%+v", resp.Calls())
	}
	got := chat.Trace()
	if got.SessionID != "s1" || len(got.Calls) != 1 {
		t.Fatalf("Trace()=%+v", got)
	}
}
