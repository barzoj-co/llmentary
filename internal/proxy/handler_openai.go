// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/barzoj-co/llmentary/internal/openaiapi"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// UpstreamCallCount is incremented each time the proxy issues an upstream HTTP
// request. Live e2e uses it as a cost guard.
var UpstreamCallCount atomic.Int64

// Compile-time assertion: adding an operationId without implementing it
// fails the build. Do not embed Unimplemented stubs.
var _ openaiapi.StrictServerInterface = (*openAIHandlers)(nil)

type openAIHandlers struct {
	s *Server
}

func (s *Server) forwardSpec(ctx context.Context) (*upstreamResponse, error) {
	ex := exchangeFrom(ctx)
	if ex == nil {
		return nil, fmt.Errorf("missing proxy exchange")
	}
	resp, err := ex.doUpstream()
	if err != nil {
		return nil, err
	}
	return &upstreamResponse{ex: ex, resp: resp}, nil
}

type exchangeKey struct{}

type proxyExchange struct {
	s             *Server
	r             *http.Request
	rawReq        []byte
	session       sessionInfo
	callID        string
	startedAt     time.Time
	span          trace.Span
	target        string
	decodeWarning string
	finished      bool
	requestVal    requestValidation
}

func withExchange(ctx context.Context, ex *proxyExchange) context.Context {
	return context.WithValue(ctx, exchangeKey{}, ex)
}

func exchangeFrom(ctx context.Context) *proxyExchange {
	ex, _ := ctx.Value(exchangeKey{}).(*proxyExchange)
	return ex
}

func matchSpecOperation(method, requestPath string) (string, bool) {
	if method != http.MethodPost {
		return "", false
	}
	clean := strings.TrimRight(requestPath, "/")
	switch {
	case strings.HasSuffix(clean, "/chat/completions"):
		return "/chat/completions", true
	case strings.HasSuffix(clean, "/responses"):
		return "/responses", true
	default:
		return "", false
	}
}

func (s *Server) handleRequestDecodeError(w http.ResponseWriter, r *http.Request, err error) {
	ex := exchangeFrom(r.Context())
	if ex == nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	ex.decodeWarning = "request does not match spec: " + err.Error()
	resp, ferr := ex.doUpstream()
	if ferr != nil {
		s.writeProxyError(w, ex, ferr)
		return
	}
	_ = (&upstreamResponse{ex: ex, resp: resp}).write(w)
}

func (s *Server) handleResponseError(w http.ResponseWriter, r *http.Request, err error) {
	ex := exchangeFrom(r.Context())
	if ex == nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	s.writeProxyError(w, ex, err)
}

func (s *Server) writeProxyError(w http.ResponseWriter, ex *proxyExchange, err error) {
	ended := s.finishWithProxyError(ex.session.ID, ex.callID, ex.startedAt, err, http.StatusBadGateway, ex.requestVal)
	if ex.span != nil {
		finishSpan(ex.span, ended, ended.EndedAt)
	}
	ex.finished = true
	http.Error(w, err.Error(), http.StatusBadGateway)
}

type upstreamResponse struct {
	ex   *proxyExchange
	resp *http.Response
}

func (u *upstreamResponse) VisitCreateChatCompletionResponse(w http.ResponseWriter) error {
	return u.write(w)
}

func (u *upstreamResponse) VisitCreateResponseResponse(w http.ResponseWriter) error {
	return u.write(w)
}

func (u *upstreamResponse) write(w http.ResponseWriter) error {
	if u == nil || u.ex == nil || u.resp == nil {
		return fmt.Errorf("empty upstream response")
	}
	defer func() { _ = u.resp.Body.Close() }()
	return u.ex.copyAndFinish(w, u.resp)
}

func (ex *proxyExchange) doUpstream() (*http.Response, error) {
	req, err := http.NewRequestWithContext(ex.r.Context(), ex.r.Method, ex.target, bytes.NewReader(ex.rawReq))
	if err != nil {
		return nil, err
	}
	req.Header = cloneHeader(ex.r.Header)
	req.Header.Del(SessionHeaderName)
	stripLlmentaryBaggage(req.Header)
	removeHopByHopHeaders(req.Header)
	if ex.s.cfg.PropagateTrace == PropagateTraceAuto {
		ex.s.tracing.Propagator.Inject(StripLlmentaryBaggageFromContext(ex.r.Context()), propagation.HeaderCarrier(req.Header))
	}
	if targetURL := req.URL; targetURL != nil {
		req.Host = targetURL.Host
	}
	UpstreamCallCount.Add(1)
	return ex.s.client.Do(req)
}

func (ex *proxyExchange) copyAndFinish(w http.ResponseWriter, resp *http.Response) error {
	var firstByte *time.Time
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	removeHopByHopHeaders(w.Header())
	w.WriteHeader(resp.StatusCode)

	var rawResp bytes.Buffer
	headerSSE := strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream")
	encoding := strings.TrimSpace(resp.Header.Get("Content-Encoding"))
	compressed := encoding != "" && !strings.EqualFold(encoding, "identity")
	var recorder *sseRecorder
	if headerSSE && !compressed {
		recorder = newSSERecorder(ex.startedAt)
	}
	_, copyErr := copyAndFlush(w, resp.Body, func(chunk []byte) {
		if firstByte == nil {
			now := time.Now().UTC()
			firstByte = &now
		}
		_, _ = rawResp.Write(chunk)
		if recorder != nil {
			recorder.Write(chunk)
		}
	})
	ex.finishCall(resp, rawResp.Bytes(), firstByte, recorder, copyErr, headerSSE)
	return nil
}

func (ex *proxyExchange) finishCall(resp *http.Response, rawBody []byte, firstByte *time.Time, recorder *sseRecorder, copyErr error, headerSSE bool) {
	if ex.finished {
		return
	}
	ex.finished = true
	endedAt := time.Now().UTC()
	respHeaders := sanitizeHeaders(resp.Header)
	parseBody := decompressIfNeeded(rawBody, respHeaders, ex.s.logger())
	stream := headerSSE || isSSE(resp.Header, parseBody)
	var events []SSEEvent
	var complete bool
	var warnings []string
	if recorder != nil {
		events, complete, warnings = recorder.Finish()
	} else if stream {
		events, complete, warnings = parseSSE(parseBody, ex.startedAt)
	}
	var endedCall Call
	ex.s.store.Finish(ex.session.ID, ex.callID, func(call *Call) {
		call.FirstByteAt = firstByte
		call.EndedAt = endedAt
		call.LatencyMS = endedAt.Sub(ex.startedAt).Milliseconds()
		call.Response = Exchange{Headers: respHeaders}
		call.RawResponse = parseBody
		call.SSEEvents = events
		call.Stream = stream
		call.HTTPStatus = resp.StatusCode
		call.RetryAfter = retryAfter(resp.Header)
		call.Normalized.ParserWarnings = append(call.Normalized.ParserWarnings, warnings...)
		call.UpstreamCalled = true
		if ex.decodeWarning != "" {
			call.Normalized.ParserWarnings = append(call.Normalized.ParserWarnings, ex.decodeWarning)
		}
		if stream && !complete {
			call.Normalized.StreamTruncated = true
		}
		if copyErr != nil && (!stream || !complete) {
			// A complete SSE stream (terminal [DONE] or Responses
			// response.completed/failed/incomplete already written) is not
			// client_aborted when the SDK closes after that event.
			call.Normalized.ClientAborted = true
			call.Normalized.ParserWarnings = append(call.Normalized.ParserWarnings, "response copy error: "+copyErr.Error())
		}
		recordRequestIDs(call, resp.Header)
		ex.s.parser.Parse(call)
		applyRequestValidation(call, ex.requestVal)
		if call.Normalized.Error != nil && call.RetryAfter != "" {
			call.Normalized.Error.RetryAfter = call.RetryAfter
		}
		if ex.s.validation != nil && resp.StatusCode < 400 {
			vios, extra, dur, trunc := ex.s.validation.checkResponse(call.APIFamily, resp.StatusCode, parseBody, stream, events)
			if call.APIFamily == "openai.responses" && ex.s.validation.linter != nil {
				extra = append(extra, ex.s.validation.linter.ConfirmStrictFallback(parseBody, omittedStrictToolIndexes(call.RawRequest))...)
			}
			mergeResponseValidation(call, vios, extra, dur, trunc)
		}
		logCall := snapshotAndCollapseViolations(call)
		endedCall = *call
		endedCall.Normalized.SpecViolations = logCall.Normalized.SpecViolations
	})
	ex.s.logger().Info("proxy response",
		"status", resp.StatusCode,
		"call_id", ex.callID,
		"session", ex.session.ID,
		"api_family", endedCall.APIFamily,
	)
	ex.s.logValidationFindings(endedCall)
	if ex.span != nil {
		finishSpan(ex.span, endedCall, endedAt)
	}
}

func (s *Server) passthrough(w http.ResponseWriter, ex *proxyExchange) {
	resp, err := ex.doUpstream()
	if err != nil {
		s.writeProxyError(w, ex, err)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	_ = ex.copyAndFinish(w, resp)
}

func (s *Server) rejectLocal(w http.ResponseWriter, ex *proxyExchange, rv requestValidation) {
	writeEnforceReject(w, rv)
	endedAt := time.Now().UTC()
	var endedCall Call
	s.store.Finish(ex.session.ID, ex.callID, func(call *Call) {
		call.EndedAt = endedAt
		call.LatencyMS = endedAt.Sub(ex.startedAt).Milliseconds()
		call.HTTPStatus = http.StatusBadRequest
		call.UpstreamCalled = false
		call.Normalized.Error = &CallError{
			HTTPStatus: http.StatusBadRequest,
			Type:       "invalid_request_error",
			Code:       rejectCode(rv.rejectKind),
			Message:    "llmentary rejected the request",
			Normalized: "bad_request",
			Source:     "proxy",
		}
		s.parser.Parse(call)
		applyRequestValidation(call, rv)
		call.Normalized.Error.Source = "proxy"
		call.Normalized.Error.HTTPStatus = http.StatusBadRequest
		logCall := snapshotAndCollapseViolations(call)
		endedCall = *call
		endedCall.Normalized.SpecViolations = logCall.Normalized.SpecViolations
	})
	s.logValidationFindings(endedCall)
	if ex.span != nil {
		finishSpan(ex.span, endedCall, endedAt)
	}
	ex.finished = true
}

func rejectCode(kind string) string {
	if kind == "ignored_field" {
		return "llmentary_ignored_field"
	}
	return "llmentary_spec_violation"
}

func omittedStrictToolIndexes(raw []byte) []int {
	var body map[string]any
	if json.Unmarshal(firstJSONBytes(raw), &body) != nil {
		return nil
	}
	tools, _ := body["tools"].([]any)
	var out []int
	for i, item := range tools {
		tool, _ := item.(map[string]any)
		if tool == nil {
			continue
		}
		if _, ok := tool["strict"]; !ok {
			out = append(out, i)
		}
	}
	return out
}

func firstJSONBytes(raw []byte) []byte {
	first, _, err := splitFirstJSON(raw)
	if err != nil {
		return raw
	}
	return first
}

func drainAndRestore(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	raw, err := io.ReadAll(r.Body)
	_ = r.Body.Close()
	if err != nil {
		return nil, err
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	return raw, nil
}
