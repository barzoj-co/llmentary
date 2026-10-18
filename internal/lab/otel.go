package lab

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/barzoj-co/llm-test-lab/internal/semconv/genai"
	"github.com/barzoj-co/llm-test-lab/pkg/llmtestlab"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

const (
	serviceName    = "llmtestlab-proxy"
	serviceVersion = "0.1.0-dev"
	scopeName      = "github.com/barzoj-co/llm-test-lab/proxy"
	scopeVersion   = "0.1.0-dev"
	genAICommit    = "f49da22"
)

type TracingRuntime struct {
	Processor  *InMemorySpanProcessor
	Provider   *sdktrace.TracerProvider
	Tracer     trace.Tracer
	Propagator propagation.TextMapPropagator
}

func NewTracingRuntime() (*TracingRuntime, error) {
	processor := NewInMemorySpanProcessor()
	res := resource.NewSchemaless(
		attribute.String("service.name", serviceName),
		attribute.String("service.version", serviceVersion),
	)
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithResource(res),
		sdktrace.WithSpanProcessor(processor),
	)
	propagator := propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{})
	return &TracingRuntime{
		Processor: processor,
		Provider:  provider,
		Tracer: provider.Tracer(
			scopeName,
			trace.WithInstrumentationVersion(scopeVersion),
			trace.WithInstrumentationAttributes(attribute.String("llmtestlab.semconv.genai_commit", genAICommit)),
		),
		Propagator: propagator,
	}, nil
}

func ExtractTraceContext(ctx context.Context, propagator propagation.TextMapPropagator, h mapCarrier) context.Context {
	return propagator.Extract(ctx, h)
}

func StripLLMTestLabBaggageFromContext(ctx context.Context) context.Context {
	bg := baggage.FromContext(ctx)
	if bg.Len() == 0 {
		return ctx
	}
	members := make([]baggage.Member, 0, bg.Len())
	for _, member := range bg.Members() {
		if strings.HasPrefix(strings.ToLower(member.Key()), "llmtestlab.") {
			continue
		}
		members = append(members, member)
	}
	next, err := baggage.New(members...)
	if err != nil {
		return ctx
	}
	return baggage.ContextWithBaggage(ctx, next)
}

type InMemorySpanProcessor struct {
	mu        sync.Mutex
	bySession map[string][]storedSpan
	active    map[trace.SpanID]string
	discard   map[trace.SpanID]struct{}
}

type storedSpan struct {
	resourceAttrs []attribute.KeyValue
	scope         instrumentation.Scope
	span          *tracepb.Span
}

func NewInMemorySpanProcessor() *InMemorySpanProcessor {
	return &InMemorySpanProcessor{
		bySession: map[string][]storedSpan{},
		active:    map[trace.SpanID]string{},
		discard:   map[trace.SpanID]struct{}{},
	}
}

func (p *InMemorySpanProcessor) OnStart(_ context.Context, s sdktrace.ReadWriteSpan) {
	sessionID := attrString(s.Attributes(), "llmtestlab.session.id")
	if sessionID == "" {
		sessionID = llmtestlab.DefaultSessionID
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.active[s.SpanContext().SpanID()] = sessionID
}

func (p *InMemorySpanProcessor) OnEnd(s sdktrace.ReadOnlySpan) {
	sessionID := attrString(s.Attributes(), "llmtestlab.session.id")
	p.mu.Lock()
	spanID := s.SpanContext().SpanID()
	_, discard := p.discard[spanID]
	delete(p.discard, spanID)
	activeSessionID := p.active[spanID]
	delete(p.active, spanID)
	p.mu.Unlock()
	if discard {
		return
	}
	if sessionID == "" {
		sessionID = activeSessionID
	}
	if sessionID == "" {
		sessionID = llmtestlab.DefaultSessionID
	}
	stored := storedSpan{
		resourceAttrs: s.Resource().Attributes(),
		scope:         s.InstrumentationScope(),
		span:          spanToProto(s),
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.bySession[sessionID] = append(p.bySession[sessionID], stored)
}

func (p *InMemorySpanProcessor) Shutdown(context.Context) error { return nil }

func (p *InMemorySpanProcessor) ForceFlush(context.Context) error { return nil }

func (p *InMemorySpanProcessor) Delete(sessionID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.bySession, sessionID)
	for spanID, activeSessionID := range p.active {
		if activeSessionID == sessionID {
			p.discard[spanID] = struct{}{}
			delete(p.active, spanID)
		}
	}
}

func (p *InMemorySpanProcessor) OTLP(sessionID string) json.RawMessage {
	p.mu.Lock()
	spans := append([]storedSpan(nil), p.bySession[sessionID]...)
	p.mu.Unlock()
	data := tracesData(spans)
	raw, err := protojson.MarshalOptions{UseProtoNames: false}.Marshal(data)
	if err != nil {
		return json.RawMessage(`{"resourceSpans":[]}`)
	}
	return fixOTLPJSONIDs(raw, data)
}

func tracesData(spans []storedSpan) *tracepb.TracesData {
	resourceAttrs := []attribute.KeyValue{
		attribute.String("service.name", serviceName),
		attribute.String("service.version", serviceVersion),
	}
	scope := instrumentation.Scope{
		Name:       scopeName,
		Version:    scopeVersion,
		Attributes: attribute.NewSet(attribute.String("llmtestlab.semconv.genai_commit", genAICommit)),
	}
	protoSpans := make([]*tracepb.Span, 0, len(spans))
	if len(spans) > 0 {
		resourceAttrs = spans[0].resourceAttrs
		scope = spans[0].scope
	}
	for _, span := range spans {
		protoSpans = append(protoSpans, span.span)
	}
	return &tracepb.TracesData{ResourceSpans: []*tracepb.ResourceSpans{{
		Resource: &resourcepb.Resource{Attributes: attributesToProto(resourceAttrs)},
		ScopeSpans: []*tracepb.ScopeSpans{{
			Scope: &commonpb.InstrumentationScope{
				Name:       scope.Name,
				Version:    scope.Version,
				Attributes: attributesToProto(scope.Attributes.ToSlice()),
			},
			Spans: protoSpans,
		}},
	}}}
}

func spanToProto(s sdktrace.ReadOnlySpan) *tracepb.Span {
	sc := s.SpanContext()
	parent := s.Parent()
	span := &tracepb.Span{
		TraceId:           traceIDBytes(sc.TraceID()),
		SpanId:            spanIDBytes(sc.SpanID()),
		TraceState:        sc.TraceState().String(),
		ParentSpanId:      spanIDBytes(parent.SpanID()),
		Flags:             spanFlags(sc, parent),
		Name:              s.Name(),
		Kind:              spanKindToProto(s.SpanKind()),
		StartTimeUnixNano: uint64(s.StartTime().UnixNano()),
		EndTimeUnixNano:   uint64(s.EndTime().UnixNano()),
		Attributes:        attributesToProto(s.Attributes()),
		Status:            statusToProto(s.Status()),
	}
	if !parent.IsValid() {
		span.ParentSpanId = nil
	}
	return span
}

func spanFlags(sc, parent trace.SpanContext) uint32 {
	flags := uint32(sc.TraceFlags())
	if parent.IsRemote() {
		flags |= 1 << 8
		flags |= 1 << 9
	}
	return flags
}

func spanKindToProto(kind trace.SpanKind) tracepb.Span_SpanKind {
	switch kind {
	case trace.SpanKindClient:
		return tracepb.Span_SPAN_KIND_CLIENT
	case trace.SpanKindServer:
		return tracepb.Span_SPAN_KIND_SERVER
	case trace.SpanKindProducer:
		return tracepb.Span_SPAN_KIND_PRODUCER
	case trace.SpanKindConsumer:
		return tracepb.Span_SPAN_KIND_CONSUMER
	case trace.SpanKindInternal:
		return tracepb.Span_SPAN_KIND_INTERNAL
	default:
		return tracepb.Span_SPAN_KIND_UNSPECIFIED
	}
}

func statusToProto(status sdktrace.Status) *tracepb.Status {
	out := &tracepb.Status{}
	if status.Code == codes.Error {
		out.Code = tracepb.Status_STATUS_CODE_ERROR
		out.Message = status.Description
	}
	if status.Code == codes.Ok {
		out.Code = tracepb.Status_STATUS_CODE_OK
	}
	return out
}

func attributesToProto(attrs []attribute.KeyValue) []*commonpb.KeyValue {
	out := make([]*commonpb.KeyValue, 0, len(attrs))
	for _, attr := range attrs {
		out = append(out, &commonpb.KeyValue{Key: string(attr.Key), Value: valueToProto(attr.Value)})
	}
	return out
}

func valueToProto(value attribute.Value) *commonpb.AnyValue {
	switch value.Type() {
	case attribute.BOOL:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: value.AsBool()}}
	case attribute.INT64:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: value.AsInt64()}}
	case attribute.FLOAT64:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_DoubleValue{DoubleValue: value.AsFloat64()}}
	case attribute.STRING:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: value.AsString()}}
	case attribute.BOOLSLICE:
		values := make([]*commonpb.AnyValue, 0, len(value.AsBoolSlice()))
		for _, item := range value.AsBoolSlice() {
			values = append(values, &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: item}})
		}
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_ArrayValue{ArrayValue: &commonpb.ArrayValue{Values: values}}}
	case attribute.INT64SLICE:
		values := make([]*commonpb.AnyValue, 0, len(value.AsInt64Slice()))
		for _, item := range value.AsInt64Slice() {
			values = append(values, &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: item}})
		}
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_ArrayValue{ArrayValue: &commonpb.ArrayValue{Values: values}}}
	case attribute.FLOAT64SLICE:
		values := make([]*commonpb.AnyValue, 0, len(value.AsFloat64Slice()))
		for _, item := range value.AsFloat64Slice() {
			values = append(values, &commonpb.AnyValue{Value: &commonpb.AnyValue_DoubleValue{DoubleValue: item}})
		}
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_ArrayValue{ArrayValue: &commonpb.ArrayValue{Values: values}}}
	case attribute.STRINGSLICE:
		values := make([]*commonpb.AnyValue, 0, len(value.AsStringSlice()))
		for _, item := range value.AsStringSlice() {
			values = append(values, &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: item}})
		}
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_ArrayValue{ArrayValue: &commonpb.ArrayValue{Values: values}}}
	case attribute.BYTESLICE:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_BytesValue{BytesValue: value.AsByteSlice()}}
	case attribute.SLICE:
		values := make([]*commonpb.AnyValue, 0, len(value.AsSlice()))
		for _, item := range value.AsSlice() {
			values = append(values, valueToProto(item))
		}
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_ArrayValue{ArrayValue: &commonpb.ArrayValue{Values: values}}}
	case attribute.MAP:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_KvlistValue{KvlistValue: &commonpb.KeyValueList{Values: attributesToProto(value.AsMap())}}}
	default:
		return &commonpb.AnyValue{}
	}
}

func traceIDBytes(id trace.TraceID) []byte {
	if !id.IsValid() {
		return nil
	}
	b, _ := hex.DecodeString(id.String())
	return b
}

func spanIDBytes(id trace.SpanID) []byte {
	if !id.IsValid() {
		return nil
	}
	b, _ := hex.DecodeString(id.String())
	return b
}

func fixOTLPJSONIDs(raw []byte, data *tracepb.TracesData) json.RawMessage {
	var doc map[string]any
	if json.Unmarshal(raw, &doc) != nil {
		return raw
	}
	resourceSpans, _ := doc["resourceSpans"].([]any)
	for resourceIndex, resource := range data.ResourceSpans {
		if resourceIndex >= len(resourceSpans) {
			break
		}
		resourceMap, _ := resourceSpans[resourceIndex].(map[string]any)
		scopeSpans, _ := resourceMap["scopeSpans"].([]any)
		for scopeIndex, scope := range resource.ScopeSpans {
			if scopeIndex >= len(scopeSpans) {
				break
			}
			scopeMap, _ := scopeSpans[scopeIndex].(map[string]any)
			spansJSON, _ := scopeMap["spans"].([]any)
			for spanIndex, span := range scope.Spans {
				if spanIndex >= len(spansJSON) {
					break
				}
				spanMap, _ := spansJSON[spanIndex].(map[string]any)
				replaceID(spanMap, "traceId", span.TraceId)
				replaceID(spanMap, "spanId", span.SpanId)
				replaceID(spanMap, "parentSpanId", span.ParentSpanId)
			}
		}
	}
	fixed, err := json.Marshal(doc)
	if err != nil {
		return raw
	}
	return fixed
}

func replaceID(span map[string]any, key string, id []byte) {
	if len(id) == 0 {
		delete(span, key)
		return
	}
	if existing, ok := span[key].(string); ok && existing == base64.StdEncoding.EncodeToString(id) {
		span[key] = hex.EncodeToString(id)
	}
}

func startSpanAttributes(call llmtestlab.Call) []attribute.KeyValue {
	model := requestModel(call)
	attrs := []attribute.KeyValue{
		attribute.String("http.request.method", call.Upstream.Method),
		attribute.String("url.full", call.Upstream.URL),
		attribute.String(genai.OperationName, "chat"),
		attribute.String(genai.ProviderName, providerName(call.Provider, call.Upstream.URL)),
		attribute.String(genai.RequestModel, model),
		attribute.String("llmtestlab.session.id", call.SessionID),
		attribute.String("llmtestlab.session.source", call.SessionSource),
		attribute.String("llmtestlab.call.id", call.ID),
		attribute.Int("llmtestlab.call.seq", call.Seq),
	}
	if host, port := serverAddressPort(call.Upstream.URL); host != "" {
		attrs = append(attrs, attribute.String("server.address", host))
		if port != 0 {
			attrs = append(attrs, attribute.Int("server.port", port))
		}
	}
	return attrs
}

func finalSpanAttributes(call llmtestlab.Call) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.Int("http.response.status_code", call.HTTPStatus),
		attribute.Int("http.request.body.size", len(call.RawRequest)),
		attribute.Int("http.response.body.size", len(call.RawResponse)),
		attribute.Int("llmtestlab.derived.version", call.Normalized.DerivedVersion),
	}
	if call.Stream {
		attrs = append(attrs, attribute.Bool(genai.RequestStream, true))
	}
	attrs = append(attrs, requestAttributes(call)...)
	attrs = append(attrs, responseAttributes(call)...)
	attrs = append(attrs, llmtestlabAttributes(call)...)
	if input := inputMessages(call); len(input) > 0 {
		attrs = append(attrs, anyAttr(genai.InputMessages, input))
	}
	if output := outputMessages(call); len(output) > 0 {
		attrs = append(attrs, anyAttr(genai.OutputMessages, output))
	}
	if call.APIFamily == "openai.responses" && call.Normalized.SystemPrompt != "" {
		attrs = append(attrs, anyAttr(genai.SystemInstructions, []any{map[string]any{"type": "text", "content": call.Normalized.SystemPrompt}}))
	}
	if defs := toolDefinitions(call); len(defs) > 0 {
		attrs = append(attrs, anyAttr(genai.ToolDefinitions, defs))
	}
	attrs = append(attrs, selectedHeaderAttrs("http.request.header.", call.Request.Headers)...)
	attrs = append(attrs, selectedHeaderAttrs("http.response.header.", call.Response.Headers)...)
	if errType, _ := errorTypeAndMessage(call); errType != "" {
		attrs = append(attrs, attribute.String("error.type", errType))
	}
	return attrs
}

func finishSpan(span trace.Span, call llmtestlab.Call, endedAt time.Time) {
	span.SetAttributes(finalSpanAttributes(call)...)
	if errType, message := errorTypeAndMessage(call); errType != "" {
		span.SetStatus(codes.Error, truncate(message, 256))
	} else if call.HTTPStatus >= 100 && call.HTTPStatus < 400 {
		span.SetStatus(codes.Unset, "")
	}
	span.End(trace.WithTimestamp(endedAt))
}

type mapCarrier map[string][]string

func (m mapCarrier) Get(key string) string {
	values := m[key]
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func (m mapCarrier) Set(key, value string) {
	m[key] = []string{value}
}

func (m mapCarrier) Keys() []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	return keys
}

func requestModel(call llmtestlab.Call) string {
	if call.Normalized.Model.Requested != "" {
		return call.Normalized.Model.Requested
	}
	var body map[string]json.RawMessage
	if json.Unmarshal(call.RawRequest, &body) == nil {
		if s := jsonString(body["model"]); s != "" {
			return s
		}
	}
	if call.Upstream.Deployment != "" {
		return call.Upstream.Deployment
	}
	return "unknown"
}

func providerName(routeName, rawURL string) string {
	u, _ := url.Parse(rawURL)
	host := strings.ToLower(u.Hostname())
	switch {
	case host == "api.openai.com":
		return "openai"
	case strings.HasSuffix(host, ".openai.azure.com"), strings.HasSuffix(host, ".cognitiveservices.azure.com"), strings.HasSuffix(host, ".services.ai.azure.com"):
		return "azure.ai.openai"
	case strings.Contains(host, "groq"):
		return "groq"
	case strings.Contains(host, "deepseek"):
		return "deepseek"
	case strings.Contains(host, "mistral"):
		return "mistral"
	case strings.Contains(host, "x.ai"):
		return "x_ai"
	case strings.Contains(host, "generativelanguage.googleapis.com"):
		return "gcp.gemini"
	case routeName != "":
		return routeName
	default:
		return "unknown"
	}
}

func serverAddressPort(rawURL string) (string, int) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", 0
	}
	host := u.Hostname()
	if host == "" {
		return "", 0
	}
	portText := u.Port()
	if portText == "" {
		if u.Scheme == "https" {
			return host, 443
		}
		if u.Scheme == "http" {
			return host, 80
		}
		return host, 0
	}
	port, _ := strconv.Atoi(portText)
	return host, port
}

func requestAttributes(call llmtestlab.Call) []attribute.KeyValue {
	var attrs []attribute.KeyValue
	if call.RetryCount != nil && *call.RetryCount > 0 {
		attrs = append(attrs, attribute.Int("http.request.resend_count", *call.RetryCount))
	}
	var body map[string]json.RawMessage
	if json.Unmarshal(call.RawRequest, &body) != nil {
		return attrs
	}
	apiType := ""
	switch call.APIFamily {
	case "openai.chat":
		apiType = "chat_completions"
	case "openai.responses":
		apiType = "responses"
	}
	if apiType != "" {
		attrs = append(attrs, attribute.String(genai.OpenAIAPIType, apiType))
	}
	copyNumber := func(jsonKey, attrKey string) {
		if raw := body[jsonKey]; len(raw) > 0 {
			if n, ok := jsonNumber(raw); ok {
				attrs = append(attrs, floatOrIntAttr(attrKey, n))
			}
		}
	}
	copyNumber("temperature", genai.RequestTemperature)
	copyNumber("top_p", genai.RequestTopP)
	copyNumber("frequency_penalty", genai.RequestFrequencyPenalty)
	copyNumber("presence_penalty", genai.RequestPresencePenalty)
	copyNumber("seed", genai.RequestSeed)
	copyNumber("n", genai.RequestChoiceCount)
	for _, key := range []string{"max_completion_tokens", "max_tokens", "max_output_tokens"} {
		if raw := body[key]; len(raw) > 0 {
			if n, ok := jsonNumber(raw); ok {
				attrs = append(attrs, floatOrIntAttr(genai.RequestMaxTokens, n))
				break
			}
		}
	}
	if raw := body["stop"]; len(raw) > 0 {
		attrs = append(attrs, anyAttr(genai.RequestStopSequences, jsonToAny(raw)))
	}
	if raw := body["reasoning_effort"]; len(raw) > 0 {
		if s := jsonString(raw); s != "" {
			attrs = append(attrs, attribute.String(genai.RequestReasoningLevel, s))
		}
	}
	if raw := body["reasoning"]; len(raw) > 0 {
		var reasoning struct {
			Effort string `json:"effort"`
		}
		if json.Unmarshal(raw, &reasoning) == nil && reasoning.Effort != "" {
			attrs = append(attrs, attribute.String(genai.RequestReasoningLevel, reasoning.Effort))
		}
	}
	if raw := body["previous_response_id"]; len(raw) > 0 {
		if s := jsonString(raw); s != "" {
			attrs = append(attrs, attribute.String(genai.RequestPreviousResponseID, s))
		}
	}
	if raw := body["conversation"]; len(raw) > 0 {
		if s := jsonString(raw); s != "" {
			attrs = append(attrs, attribute.String(genai.ConversationID, s))
		}
	}
	if raw := body["response_format"]; len(raw) > 0 {
		if outputType := outputTypeFromFormat(raw); outputType != "" {
			attrs = append(attrs, attribute.String(genai.OutputType, outputType))
		}
	} else if raw := body["text"]; len(raw) > 0 {
		if outputType := outputTypeFromText(raw); outputType != "" {
			attrs = append(attrs, attribute.String(genai.OutputType, outputType))
		}
	}
	if raw := body["service_tier"]; len(raw) > 0 {
		if s := jsonString(raw); s != "" && s != "auto" {
			attrs = append(attrs, attribute.String(genai.OpenAIRequestServiceTier, s))
		}
	}
	return attrs
}

func responseAttributes(call llmtestlab.Call) []attribute.KeyValue {
	var attrs []attribute.KeyValue
	if id := call.RequestIDs["body.id"]; id != "" {
		attrs = append(attrs, attribute.String(genai.ResponseID, id))
	}
	if call.Normalized.Model.Returned != "" {
		attrs = append(attrs, attribute.String(genai.ResponseModel, call.Normalized.Model.Returned))
	}
	if call.APIFamily == "openai.responses" && call.Normalized.FinishReason != "" {
		attrs = append(attrs, attribute.StringSlice(genai.ResponseFinishReasons, []string{call.Normalized.FinishReason}))
	} else if call.Normalized.NativeFinishReason != "" {
		attrs = append(attrs, attribute.StringSlice(genai.ResponseFinishReasons, []string{call.Normalized.NativeFinishReason}))
	} else if call.Normalized.FinishReason != "" {
		attrs = append(attrs, attribute.StringSlice(genai.ResponseFinishReasons, []string{call.Normalized.FinishReason}))
	}
	if usage := call.Normalized.Usage; usage != nil {
		if usage.InputTokens != nil {
			attrs = append(attrs, attribute.Int(genai.UsageInputTokens, *usage.InputTokens))
		}
		if usage.OutputTokens != nil {
			attrs = append(attrs, attribute.Int(genai.UsageOutputTokens, *usage.OutputTokens))
		}
		if usage.CachedInputTokens != nil && *usage.CachedInputTokens != 0 {
			attrs = append(attrs, attribute.Int(genai.UsageCacheReadInputTokens, *usage.CachedInputTokens))
		}
		if usage.ReasoningTokens != nil && *usage.ReasoningTokens != 0 {
			attrs = append(attrs, attribute.Int(genai.UsageReasoningOutputTokens, *usage.ReasoningTokens))
		}
	}
	if call.Stream && call.FirstByteAt != nil {
		attrs = append(attrs, attribute.Float64(genai.ResponseTimeToFirstChunk, call.FirstByteAt.Sub(call.StartedAt).Seconds()))
	}
	var body map[string]json.RawMessage
	if json.Unmarshal(call.RawResponse, &body) == nil {
		if raw := body["system_fingerprint"]; len(raw) > 0 {
			if s := jsonString(raw); s != "" {
				attrs = append(attrs, attribute.String(genai.OpenAIResponseFingerprint, s))
			}
		}
		if raw := body["service_tier"]; len(raw) > 0 {
			if s := jsonString(raw); s != "" {
				attrs = append(attrs, attribute.String(genai.OpenAIResponseServiceTier, s))
			}
		}
	}
	return attrs
}

func llmtestlabAttributes(call llmtestlab.Call) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.Int("llmtestlab.prompt.chars", promptTextChars(call)),
		attribute.Int("llmtestlab.prompt.message_count", call.Normalized.MessagesCount),
		attribute.StringSlice("llmtestlab.finish_reasons.normalized", []string{call.Normalized.FinishReason}),
		attribute.Int("llmtestlab.stream.chunk_count", len(call.SSEEvents)),
		attribute.Bool("llmtestlab.stream.truncated", call.Normalized.StreamTruncated),
		attribute.Bool("llmtestlab.client.aborted", call.Normalized.ClientAborted),
	}
	if call.Normalized.SystemPromptHash != "" {
		attrs = append(attrs, attribute.String("llmtestlab.system_prompt.hash", ensureSHA(call.Normalized.SystemPromptHash)))
	}
	if len(call.Normalized.ToolsDeclared) > 0 {
		if b, err := json.Marshal(call.Normalized.ToolsDeclared); err == nil {
			attrs = append(attrs, attribute.String("llmtestlab.tool_definitions.hash", "sha256:"+hashBytes(b)))
		}
	}
	if len(call.Normalized.ToolCalls) > 0 {
		names := make([]string, 0, len(call.Normalized.ToolCalls))
		for _, tool := range call.Normalized.ToolCalls {
			names = append(names, tool.Name)
		}
		attrs = append(attrs, attribute.StringSlice("llmtestlab.tool_calls.names", names))
	}
	if call.Upstream.APIVersion != "" {
		attrs = append(attrs, attribute.String("llmtestlab.azure.api_version", call.Upstream.APIVersion))
	}
	if call.Upstream.Deployment != "" {
		attrs = append(attrs, attribute.String("llmtestlab.azure.deployment", call.Upstream.Deployment))
	}
	if len(call.Normalized.ParserWarnings) > 0 {
		attrs = append(attrs, attribute.StringSlice("llmtestlab.parser.warnings", call.Normalized.ParserWarnings))
	}
	if call.Normalized.Error != nil && call.Normalized.Error.MidStream {
		attrs = append(attrs, attribute.Bool("llmtestlab.error.mid_stream", true))
	}
	return attrs
}

func errorTypeAndMessage(call llmtestlab.Call) (string, string) {
	if call.Normalized.ClientAborted {
		return "client_aborted", "client aborted the stream"
	}
	if err := call.Normalized.Error; err != nil {
		if err.Code != "" {
			return err.Code, err.Message
		}
		if err.Type != "" {
			return err.Type, err.Message
		}
		if call.HTTPStatus != 0 {
			return strconv.Itoa(call.HTTPStatus), err.Message
		}
		return "_OTHER", err.Message
	}
	if call.Normalized.StreamTruncated {
		return "stream_truncated", "stream ended before terminal marker"
	}
	if call.HTTPStatus >= 400 {
		return strconv.Itoa(call.HTTPStatus), ""
	}
	return "", ""
}

func selectedHeaderAttrs(prefix string, headers map[string][]string) []attribute.KeyValue {
	var attrs []attribute.KeyValue
	for key, values := range headers {
		lower := strings.ToLower(key)
		if lower == "x-request-id" || lower == "apim-request-id" || lower == "retry-after" || lower == "retry-after-ms" ||
			lower == "openai-processing-ms" || strings.HasPrefix(lower, "x-stainless-") {
			attrs = append(attrs, attribute.StringSlice(prefix+lower, values))
		}
	}
	return attrs
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}

func ensureSHA(value string) string {
	if strings.HasPrefix(value, "sha256:") {
		return value
	}
	return "sha256:" + value
}

func jsonNumber(raw json.RawMessage) (float64, bool) {
	var n float64
	if json.Unmarshal(raw, &n) == nil {
		return n, true
	}
	return 0, false
}

func floatOrIntAttr(key string, n float64) attribute.KeyValue {
	if n == float64(int64(n)) {
		return attribute.Int64(key, int64(n))
	}
	return attribute.Float64(key, n)
}

func jsonString(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return ""
}

func jsonToAny(raw json.RawMessage) any {
	var v any
	if json.Unmarshal(raw, &v) == nil {
		return v
	}
	return string(raw)
}

func outputTypeFromFormat(raw json.RawMessage) string {
	var format struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &format) != nil {
		return ""
	}
	switch format.Type {
	case "json_object", "json_schema":
		return "json"
	case "text":
		return "text"
	default:
		return format.Type
	}
}

func outputTypeFromText(raw json.RawMessage) string {
	var wrapper struct {
		Format json.RawMessage `json:"format"`
	}
	if json.Unmarshal(raw, &wrapper) == nil && len(wrapper.Format) > 0 {
		return outputTypeFromFormat(wrapper.Format)
	}
	return ""
}

func inputMessages(call llmtestlab.Call) []any {
	var body map[string]json.RawMessage
	if json.Unmarshal(call.RawRequest, &body) != nil {
		return nil
	}
	switch call.APIFamily {
	case "openai.chat":
		return chatInputMessages(body["messages"])
	case "openai.responses":
		return responsesInputMessages(body["input"])
	}
	return nil
}

func chatInputMessages(raw json.RawMessage) []any {
	var messages []struct {
		Role       string          `json:"role"`
		Content    json.RawMessage `json:"content"`
		ToolCallID string          `json:"tool_call_id"`
		ToolCalls  []struct {
			ID       string `json:"id"`
			Type     string `json:"type"`
			Function struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"function"`
		} `json:"tool_calls"`
	}
	if json.Unmarshal(raw, &messages) != nil {
		return nil
	}
	out := make([]any, 0, len(messages))
	for _, message := range messages {
		parts := []any{}
		if message.Role == "tool" {
			parts = append(parts, map[string]any{"type": "tool_call_response", "id": message.ToolCallID, "response": contentString(message.Content)})
		} else if text := contentString(message.Content); text != "" {
			parts = append(parts, map[string]any{"type": "text", "content": text})
		}
		for _, tool := range message.ToolCalls {
			args := jsonToAny(json.RawMessage(tool.Function.Arguments))
			parts = append(parts, map[string]any{"type": "tool_call", "id": tool.ID, "name": tool.Function.Name, "arguments": args})
		}
		out = append(out, map[string]any{"role": message.Role, "parts": parts})
	}
	return out
}

func responsesInputMessages(raw json.RawMessage) []any {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))
	if len(raw) == 0 {
		return nil
	}
	if raw[0] == '"' {
		return []any{map[string]any{"role": "user", "parts": []any{map[string]any{"type": "text", "content": jsonString(raw)}}}}
	}
	var items []map[string]any
	if json.Unmarshal(raw, &items) != nil {
		return nil
	}
	out := make([]any, 0, len(items))
	for _, item := range items {
		if item["type"] == "function_call_output" {
			out = append(out, map[string]any{"role": "tool", "parts": []any{map[string]any{"type": "tool_call_response", "id": item["call_id"], "response": item["output"]}}})
			continue
		}
		role, _ := item["role"].(string)
		if role == "" {
			role = "user"
		}
		if content, ok := item["content"]; ok {
			out = append(out, map[string]any{"role": role, "parts": []any{map[string]any{"type": "text", "content": content}}})
		}
	}
	return out
}

func outputMessages(call llmtestlab.Call) []any {
	parts := []any{}
	if call.Normalized.OutputText != "" {
		parts = append(parts, map[string]any{"type": "text", "content": call.Normalized.OutputText})
	}
	for _, tool := range call.Normalized.ToolCalls {
		args := any(tool.ArgumentsRaw)
		if len(tool.Arguments) > 0 {
			args = jsonToAny(tool.Arguments)
		}
		partType := "tool_call"
		if strings.HasPrefix(tool.Kind, "builtin:") {
			partType = "server_tool_call"
		}
		parts = append(parts, map[string]any{"type": partType, "id": tool.ID, "name": tool.Name, "arguments": args})
	}
	if len(parts) == 0 {
		return nil
	}
	return []any{map[string]any{"role": "assistant", "parts": parts}}
}

func toolDefinitions(call llmtestlab.Call) []any {
	out := make([]any, 0, len(call.Normalized.ToolsDeclared))
	for _, tool := range call.Normalized.ToolsDeclared {
		out = append(out, map[string]any{"type": tool.Type, "name": tool.Name})
	}
	return out
}

func promptTextChars(call llmtestlab.Call) int {
	var body map[string]json.RawMessage
	if json.Unmarshal(call.RawRequest, &body) != nil {
		return call.Normalized.PromptChars
	}
	total := 0
	switch call.APIFamily {
	case "openai.chat":
		var messages []struct {
			Content json.RawMessage `json:"content"`
		}
		if json.Unmarshal(body["messages"], &messages) == nil {
			for _, message := range messages {
				total += len([]rune(contentString(message.Content)))
			}
		}
	case "openai.responses":
		if instructions := jsonString(body["instructions"]); instructions != "" {
			total += len([]rune(instructions))
		}
		input := body["input"]
		if s := jsonString(input); s != "" {
			total += len([]rune(s))
		} else {
			var items []struct {
				Content json.RawMessage `json:"content"`
			}
			if json.Unmarshal(input, &items) == nil {
				for _, item := range items {
					total += len([]rune(contentString(item.Content)))
				}
			}
		}
	}
	if total == 0 {
		return call.Normalized.PromptChars
	}
	return total
}

func anyAttr(key string, value any) attribute.KeyValue {
	return attribute.KeyValue{Key: attribute.Key(key), Value: anyAttributeValue(value)}
}

func anyAttributeValue(value any) attribute.Value {
	switch v := value.(type) {
	case nil:
		return attribute.StringValue("")
	case string:
		return attribute.StringValue(v)
	case bool:
		return attribute.BoolValue(v)
	case int:
		return attribute.IntValue(v)
	case int64:
		return attribute.Int64Value(v)
	case float64:
		return attribute.Float64Value(v)
	case []any:
		values := make([]attribute.Value, 0, len(v))
		for _, item := range v {
			values = append(values, anyAttributeValue(item))
		}
		return attribute.SliceValue(values...)
	case map[string]any:
		values := make([]attribute.KeyValue, 0, len(v))
		for key, item := range v {
			values = append(values, attribute.KeyValue{Key: attribute.Key(key), Value: anyAttributeValue(item)})
		}
		return attribute.MapValue(values...)
	default:
		b, _ := json.Marshal(v)
		return attribute.StringValue(string(b))
	}
}

func attrString(attrs []attribute.KeyValue, key string) string {
	for _, attr := range attrs {
		if string(attr.Key) == key && attr.Value.Type() == attribute.STRING {
			return attr.Value.AsString()
		}
	}
	return ""
}
