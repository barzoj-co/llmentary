// SPDX-License-Identifier: AGPL-3.0-only

package tracetest

import (
	"fmt"
	"strings"

	"github.com/barzoj-co/llmentary/internal/proxy"
)

type Trace struct {
	SessionID string
	Calls     []proxy.Call
}

func (tr Trace) Query() Query {
	return Query{trace: tr, calls: append([]proxy.Call(nil), tr.Calls...)}
}

func (tr Trace) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "session %s: %d call(s)\n", tr.SessionID, len(tr.Calls))
	for _, call := range tr.Calls {
		fmt.Fprintf(&b, "  #%d %s %s status=%d finish=%s model=%s/%s latency=%dms retry=",
			call.Seq, call.Provider, call.APIFamily, call.HTTPStatus, call.Normalized.FinishReason,
			call.Normalized.Model.Requested, call.Normalized.Model.Returned, call.LatencyMS)
		if call.RetryCount == nil {
			b.WriteString("unknown")
		} else {
			fmt.Fprintf(&b, "%d", *call.RetryCount)
		}
		b.WriteByte('\n')
		for _, tool := range call.Normalized.ToolCalls {
			fmt.Fprintf(&b, "    tool[%d] %s args=%s\n", tool.Index, tool.Name, strings.TrimSpace(tool.ArgumentsRaw))
		}
		if call.Normalized.Error != nil {
			fmt.Fprintf(&b, "    error type=%s code=%s message=%s mid_stream=%v\n", call.Normalized.Error.Type, call.Normalized.Error.Code, call.Normalized.Error.Message, call.Normalized.Error.MidStream)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

type Query struct {
	trace Trace
	calls []proxy.Call
}

func (q Query) Calls() []proxy.Call { return append([]proxy.Call(nil), q.calls...) }

func (q Query) Trace() Trace {
	return Trace{SessionID: q.trace.SessionID, Calls: q.Calls()}
}

func (q Query) APIType(apiType string) Query {
	return q.keep(func(c proxy.Call) bool {
		return c.APIFamily == apiType || strings.TrimPrefix(c.APIFamily, "openai.") == apiType
	})
}

func (q Query) keep(fn func(proxy.Call) bool) Query {
	out := make([]proxy.Call, 0, len(q.calls))
	for _, call := range q.calls {
		if fn(call) {
			out = append(out, call)
		}
	}
	return Query{trace: q.trace, calls: out}
}
