// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace"
)

func TestErrorTypeAndMessageCompleteStream(t *testing.T) {
	call := Call{
		HTTPStatus: http.StatusOK,
		Stream:     true,
		Normalized: Normalized{FinishReason: "stop"},
	}
	if typ, msg := errorTypeAndMessage(call); typ != "" || msg != "" {
		t.Fatalf("complete stream: type=%q msg=%q", typ, msg)
	}
}

func TestErrorTypeAndMessageClientAborted(t *testing.T) {
	call := Call{Normalized: Normalized{ClientAborted: true, FinishReason: "stop"}}
	typ, msg := errorTypeAndMessage(call)
	if typ != "client_aborted" {
		t.Fatalf("type=%q want client_aborted msg=%q", typ, msg)
	}
}

func TestFinishSpanCompleteStreamHasNoError(t *testing.T) {
	rt, err := NewTracingRuntime()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Provider.Shutdown(context.Background()) })
	call := Call{
		SessionID:  "otel-complete",
		HTTPStatus: http.StatusOK,
		Stream:     true,
		Normalized: Normalized{FinishReason: "stop"},
	}
	ctx := context.Background()
	_, span := rt.Tracer.Start(ctx, "chat", trace.WithAttributes(startSpanAttributes(call)...))
	finishSpan(span, call, time.Now().UTC())
	raw := rt.Processor.OTLP(call.SessionID)
	var traces map[string]any
	if err := json.Unmarshal(raw, &traces); err != nil {
		t.Fatal(err)
	}
	resourceSpans, _ := traces["resourceSpans"].([]any)
	if len(resourceSpans) == 0 {
		t.Fatalf("no resourceSpans: %s", raw)
	}
	rs, _ := resourceSpans[0].(map[string]any)
	scopeSpans, _ := rs["scopeSpans"].([]any)
	if len(scopeSpans) == 0 {
		t.Fatalf("no scopeSpans: %s", raw)
	}
	ss, _ := scopeSpans[0].(map[string]any)
	spans, _ := ss["spans"].([]any)
	if len(spans) != 1 {
		t.Fatalf("spans=%d raw=%s", len(spans), raw)
	}
	spanObj, _ := spans[0].(map[string]any)
	if status, _ := spanObj["status"].(map[string]any); status != nil {
		code := status["code"]
		if code != nil && code != float64(0) && code != "STATUS_CODE_UNSET" && code != 0 {
			t.Fatalf("span status=%+v", status)
		}
	}
	for _, rawAttr := range asAnySlice(spanObj["attributes"]) {
		attr, _ := rawAttr.(map[string]any)
		if attr["key"] == "error.type" {
			t.Fatalf("error.type present: %+v", attr)
		}
	}
}

func asAnySlice(v any) []any {
	s, _ := v.([]any)
	return s
}
