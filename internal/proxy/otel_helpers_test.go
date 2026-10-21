// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

func TestSpanKindAndValueToProto(t *testing.T) {
	t.Parallel()
	cases := map[trace.SpanKind]tracepb.Span_SpanKind{
		trace.SpanKindClient:      tracepb.Span_SPAN_KIND_CLIENT,
		trace.SpanKindServer:      tracepb.Span_SPAN_KIND_SERVER,
		trace.SpanKindProducer:    tracepb.Span_SPAN_KIND_PRODUCER,
		trace.SpanKindConsumer:    tracepb.Span_SPAN_KIND_CONSUMER,
		trace.SpanKindInternal:    tracepb.Span_SPAN_KIND_INTERNAL,
		trace.SpanKindUnspecified: tracepb.Span_SPAN_KIND_UNSPECIFIED,
	}
	for kind, want := range cases {
		if got := spanKindToProto(kind); got != want {
			t.Fatalf("kind %v: %v", kind, got)
		}
	}

	attrs := []attribute.KeyValue{
		attribute.Bool("b", true),
		attribute.Int64("i", 3),
		attribute.Float64("f", 1.5),
		attribute.String("s", "x"),
		attribute.BoolSlice("bs", []bool{true, false}),
		attribute.Int64Slice("is", []int64{1}),
		attribute.Float64Slice("fs", []float64{1.25}),
		attribute.StringSlice("ss", []string{"a"}),
	}
	out := attributesToProto(attrs)
	if len(out) != len(attrs) {
		t.Fatalf("len=%d", len(out))
	}
	ok := statusToProto(sdktrace.Status{Code: codes.Ok})
	if ok.Code != tracepb.Status_STATUS_CODE_OK {
		t.Fatalf("ok=%v", ok)
	}
	err := statusToProto(sdktrace.Status{Code: codes.Error, Description: "boom"})
	if err.Code != tracepb.Status_STATUS_CODE_ERROR || err.Message != "boom" {
		t.Fatalf("err=%v", err)
	}
}

func TestMapCarrierAndExtractTraceContext(t *testing.T) {
	t.Parallel()
	m := mapCarrier{}
	if m.Get("traceparent") != "" {
		t.Fatal("empty get")
	}
	m.Set("traceparent", "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01")
	m.Set("baggage", "llmentary.session.id=s1,other=keep")
	if m.Get("traceparent") == "" || len(m.Keys()) != 2 {
		t.Fatalf("%v keys=%v", m, m.Keys())
	}
	prop := propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{})
	ctx := ExtractTraceContext(context.Background(), prop, m)
	if !trace.SpanContextFromContext(ctx).IsValid() {
		t.Fatal("expected extracted span context")
	}
	stripped := StripLlmentaryBaggageFromContext(ctx)
	bg := baggage.FromContext(stripped)
	if bg.Member("llmentary.session.id").Value() != "" {
		t.Fatalf("llmentary baggage kept: %s", bg.String())
	}
	if bg.Member("other").Value() != "keep" {
		t.Fatalf("other=%s", bg.String())
	}
	if ctx2 := StripLlmentaryBaggageFromContext(context.Background()); ctx2 != context.Background() {
		t.Fatal("empty baggage should be unchanged")
	}
}

func TestInMemoryProcessorForceFlushDeleteAndOTLP(t *testing.T) {
	rt, err := NewTracingRuntime()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Provider.Shutdown(context.Background()) })
	if err := rt.Processor.ForceFlush(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, span := rt.Tracer.Start(context.Background(), "chat", trace.WithAttributes(
		attribute.String("llmentary.session.id", "otel-del"),
	))
	_ = ctx
	rt.Processor.Delete("otel-del")
	span.End()
	raw := rt.Processor.OTLP("otel-del")
	if !strings.Contains(string(raw), `"resourceSpans"`) {
		t.Fatalf("otlp=%s", raw)
	}
}

func TestProviderNameAndServerAddressPort(t *testing.T) {
	t.Parallel()
	cases := []struct {
		url, route, want string
	}{
		{"https://api.openai.com/v1", "", "openai"},
		{"https://east.openai.azure.com", "", "azure.ai.openai"},
		{"https://x.cognitiveservices.azure.com", "", "azure.ai.openai"},
		{"https://x.services.ai.azure.com", "", "azure.ai.openai"},
		{"https://api.groq.com", "", "groq"},
		{"https://api.deepseek.com", "", "deepseek"},
		{"https://api.mistral.ai", "", "mistral"},
		{"https://api.x.ai", "", "x_ai"},
		{"https://generativelanguage.googleapis.com", "", "gcp.gemini"},
		{"https://other.example", "custom", "custom"},
		{"https://other.example", "", "unknown"},
	}
	for _, tc := range cases {
		if got := providerName(tc.route, tc.url); got != tc.want {
			t.Fatalf("%s route=%q got %q want %q", tc.url, tc.route, got, tc.want)
		}
	}
	host, port := serverAddressPort("https://api.openai.com/v1")
	if host != "api.openai.com" || port != 443 {
		t.Fatalf("%s %d", host, port)
	}
	host, port = serverAddressPort("http://localhost:8080/v1")
	if host != "localhost" || port != 8080 {
		t.Fatalf("%s %d", host, port)
	}
	host, port = serverAddressPort("http://example.com/v1")
	if host != "example.com" || port != 80 {
		t.Fatalf("http default %s %d", host, port)
	}
	if h, p := serverAddressPort(":"); h != "" || p != 0 {
		t.Fatalf("bad url %s %d", h, p)
	}
	if h, p := serverAddressPort("mailto:x@y"); h != "" || p != 0 {
		t.Fatalf("mailto %s %d", h, p)
	}
}

func TestRequestModelAndOTELHelpers(t *testing.T) {
	t.Parallel()
	if requestModel(Call{Normalized: Normalized{Model: Model{Requested: "m"}}}) != "m" {
		t.Fatal("requested")
	}
	if requestModel(Call{RawRequest: []byte(`{"model":"from-body"}`)}) != "from-body" {
		t.Fatal("body")
	}
	if requestModel(Call{Upstream: Upstream{Deployment: "dep"}}) != "dep" {
		t.Fatal("deployment")
	}
	if requestModel(Call{}) != "unknown" {
		t.Fatal("unknown")
	}
	if truncate("abcd", 3) != "abc" || truncate("ab", 3) != "ab" {
		t.Fatal("truncate")
	}
	if ensureSHA("dead") != "sha256:dead" || ensureSHA("sha256:x") != "sha256:x" {
		t.Fatal("ensureSHA")
	}
	if n, ok := jsonNumber(json.RawMessage(`1.25`)); !ok || n != 1.25 {
		t.Fatal("jsonNumber")
	}
	if _, ok := jsonNumber(json.RawMessage(`"x"`)); ok {
		t.Fatal("jsonNumber string")
	}
	if floatOrIntAttr("k", 3).Value.Type() != attribute.INT64 {
		t.Fatal("int attr")
	}
	if floatOrIntAttr("k", 1.5).Value.Type() != attribute.FLOAT64 {
		t.Fatal("float attr")
	}
	if jsonToAny(json.RawMessage(`{"a":1}`)) == nil {
		t.Fatal("jsonToAny")
	}
	if jsonToAny(json.RawMessage(`{`)) != "{" {
		t.Fatal("jsonToAny invalid")
	}
	if outputTypeFromFormat(json.RawMessage(`{"type":"json_schema"}`)) != "json" {
		t.Fatal("json schema")
	}
	if outputTypeFromFormat(json.RawMessage(`{"type":"text"}`)) != "text" {
		t.Fatal("text")
	}
	if outputTypeFromFormat(json.RawMessage(`{"type":"other"}`)) != "other" {
		t.Fatal("other")
	}
	if outputTypeFromText(json.RawMessage(`{"format":{"type":"json_object"}}`)) != "json" {
		t.Fatal("text wrapper")
	}
	if outputTypeFromText(json.RawMessage(`{}`)) != "" {
		t.Fatal("empty text")
	}
}

func TestInputOutputMessagesAndAnyAttribute(t *testing.T) {
	t.Parallel()
	chat := Call{
		APIFamily:  "openai.chat",
		RawRequest: []byte(`{"messages":[{"role":"user","content":"hi"},{"role":"tool","tool_call_id":"t1","content":"ok"},{"role":"assistant","tool_calls":[{"id":"c1","type":"function","function":{"name":"f","arguments":"{\"a\":1}"}}]}]}`),
		Normalized: Normalized{OutputText: "out", ToolCalls: []ToolCall{{ID: "c1", Name: "f", Kind: "function", Arguments: json.RawMessage(`{"a":1}`)}, {ID: "b1", Name: "web", Kind: "builtin:web_search", ArgumentsRaw: "{}"}}, ToolsDeclared: []ToolDecl{{Type: "function", Name: "f"}}},
	}
	in := inputMessages(chat)
	if len(in) != 3 {
		t.Fatalf("chat input=%v", in)
	}
	out := outputMessages(chat)
	if len(out) != 1 {
		t.Fatalf("output=%v", out)
	}
	if defs := toolDefinitions(chat); len(defs) != 1 {
		t.Fatalf("defs=%v", defs)
	}
	resp := Call{APIFamily: "openai.responses", RawRequest: []byte(`{"input":"hello"}`)}
	if len(responsesInputMessages(json.RawMessage(`"hello"`))) != 1 {
		t.Fatal("string input")
	}
	items := responsesInputMessages(json.RawMessage(`[{"type":"function_call_output","call_id":"c","output":"x"},{"role":"user","content":"q"}]`))
	if len(items) != 2 {
		t.Fatalf("items=%v", items)
	}
	if inputMessages(resp) == nil {
		t.Fatal("resp input")
	}
	if inputMessages(Call{RawRequest: []byte(`{`)}) != nil {
		t.Fatal("bad json")
	}

	_ = anyAttributeValue(nil)
	_ = anyAttributeValue("s")
	_ = anyAttributeValue(true)
	_ = anyAttributeValue(3)
	_ = anyAttributeValue(int64(4))
	_ = anyAttributeValue(1.5)
	_ = anyAttributeValue([]any{"a", 1})
	_ = anyAttributeValue(map[string]any{"k": "v"})
	_ = anyAttributeValue(struct{ X int }{1})
	if attrString([]attribute.KeyValue{attribute.Int("n", 1), attribute.String("k", "v")}, "k") != "v" {
		t.Fatal("attrString")
	}
	if attrString(nil, "k") != "" {
		t.Fatal("missing attr")
	}
}

func TestErrorTypeAndMessageFallbacks(t *testing.T) {
	t.Parallel()
	typ, _ := errorTypeAndMessage(Call{Normalized: Normalized{Error: &CallError{Type: "t", Message: "m"}}})
	if typ != "t" {
		t.Fatalf("type=%s", typ)
	}
	typ, _ = errorTypeAndMessage(Call{HTTPStatus: 502, Normalized: Normalized{Error: &CallError{Message: "m"}}})
	if typ != "502" {
		t.Fatalf("status=%s", typ)
	}
	typ, _ = errorTypeAndMessage(Call{Normalized: Normalized{Error: &CallError{Message: "m"}}})
	if typ != "_OTHER" {
		t.Fatalf("other=%s", typ)
	}
	typ, _ = errorTypeAndMessage(Call{Normalized: Normalized{StreamTruncated: true}})
	if typ != "stream_truncated" {
		t.Fatalf("trunc=%s", typ)
	}
	typ, _ = errorTypeAndMessage(Call{HTTPStatus: 404})
	if typ != "404" {
		t.Fatalf("http=%s", typ)
	}
}

func TestRequestAttributesCopyNumbers(t *testing.T) {
	t.Parallel()
	retry := 2
	call := Call{
		APIFamily:  "openai.chat",
		RetryCount: &retry,
		RawRequest: []byte(`{
			"temperature":0.5,"top_p":1,"frequency_penalty":0,"presence_penalty":0,
			"seed":1,"n":1,"max_tokens":16,"stop":["END"],"reasoning_effort":"low",
			"previous_response_id":"p","conversation":"c","response_format":{"type":"json_object"},
			"service_tier":"default"
		}`),
	}
	attrs := requestAttributes(call)
	if len(attrs) < 8 {
		t.Fatalf("attrs=%v", attrs)
	}
	call.APIFamily = "openai.responses"
	call.RawRequest = []byte(`{"reasoning":{"effort":"high"},"text":{"format":{"type":"text"}},"max_output_tokens":8}`)
	attrs = requestAttributes(call)
	if len(attrs) == 0 {
		t.Fatal("responses attrs")
	}
}

func TestTraceIDBytesInvalid(t *testing.T) {
	t.Parallel()
	if traceIDBytes(trace.TraceID{}) != nil {
		t.Fatal("invalid trace")
	}
	if spanIDBytes(trace.SpanID{}) != nil {
		t.Fatal("invalid span")
	}
	replaceID(map[string]any{"traceId": "x"}, "traceId", nil)
}
