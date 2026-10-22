// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
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
	s               *Server
	r               *http.Request
	rawReq          []byte
	session         sessionInfo
	callID          string
	startedAt       time.Time
	span            trace.Span
	target          string
	decodeWarning   string
	finished        bool
	requestVal      requestValidation
	wroteRequest    atomic.Bool
	seq             int
	upstreamCancel  context.CancelFunc
	idleTimeoutFire atomic.Bool
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
	if ex != nil && errors.Is(err, context.Canceled) && ex.r != nil && ex.r.Context().Err() != nil {
		ended := s.finishWithClientAbort(ex, err)
		if ex.span != nil {
			finishSpan(ex.span, ended, ended.EndedAt)
		}
		ex.finished = true
		return
	}
	if ex != nil && errors.Is(err, ErrUpstreamHeaderTimeout) {
		ended := s.finishWithUpstreamHeaderTimeout(ex, err)
		s.logProxyResponse(ex, ended, http.StatusGatewayTimeout)
		if ex.span != nil {
			finishSpan(ex.span, ended, ended.EndedAt)
		}
		ex.finished = true
		writeLocalOpenAIError(w, http.StatusGatewayTimeout, "upstream_timeout", err.Error())
		return
	}
	ended := s.finishWithProxyError(ex.session.ID, ex.callID, ex.startedAt, err, http.StatusBadGateway, ex.requestVal, ex.wroteRequest.Load())
	s.logProxyResponse(ex, ended, http.StatusBadGateway)
	if ex.span != nil {
		finishSpan(ex.span, ended, ended.EndedAt)
	}
	ex.finished = true
	http.Error(w, err.Error(), http.StatusBadGateway)
}

func (s *Server) finishWithClientAbort(ex *proxyExchange, err error) Call {
	endedAt := time.Now().UTC()
	var endedCall Call
	s.store.Finish(ex.session.ID, ex.callID, func(call *Call) {
		call.EndedAt = endedAt
		call.LatencyMS = endedAt.Sub(ex.startedAt).Milliseconds()
		call.HTTPStatus = 0
		call.Stream = requestStreamFlag(ex.rawReq)
		call.UpstreamCalled = ex.wroteRequest.Load()
		call.ClientAborted = true
		call.AbortPhase = AbortPhaseBeforeHeaders
		call.Normalized.DerivedVersion = derivedVersion
		call.Normalized.ClientAborted = true
		call.Normalized.FinishReason = "cancelled"
		call.Normalized.Error = &CallError{
			Message:    err.Error(),
			Normalized: "client_aborted",
		}
		applyRequestValidation(call, ex.requestVal)
		logCall := snapshotAndCollapseViolations(call)
		endedCall = *call
		endedCall.Normalized.SpecViolations = logCall.Normalized.SpecViolations
	})
	s.logProxyResponse(ex, endedCall, "-", "client_aborted", true, "phase", AbortPhaseBeforeHeaders)
	s.logValidationFindings(endedCall)
	return endedCall
}

func (s *Server) finishWithUpstreamHeaderTimeout(ex *proxyExchange, err error) Call {
	endedAt := time.Now().UTC()
	var endedCall Call
	s.store.Finish(ex.session.ID, ex.callID, func(call *Call) {
		call.EndedAt = endedAt
		call.LatencyMS = endedAt.Sub(ex.startedAt).Milliseconds()
		call.HTTPStatus = http.StatusGatewayTimeout
		call.Stream = requestStreamFlag(ex.rawReq)
		call.UpstreamCalled = ex.wroteRequest.Load()
		s.parser.Parse(call)
		call.Normalized.Error = &CallError{
			HTTPStatus: http.StatusGatewayTimeout,
			Type:       "upstream_timeout",
			Code:       "upstream_timeout",
			Message:    err.Error(),
			Normalized: "upstream_timeout",
			Source:     "proxy",
		}
		call.Normalized.FinishReason = "error"
		applyRequestValidation(call, ex.requestVal)
		logCall := snapshotAndCollapseViolations(call)
		endedCall = *call
		endedCall.Normalized.SpecViolations = logCall.Normalized.SpecViolations
	})
	s.logValidationFindings(endedCall)
	return endedCall
}

func requestStreamFlag(raw []byte) bool {
	var body map[string]any
	if json.Unmarshal(firstJSONBytes(raw), &body) != nil {
		return false
	}
	stream, ok := body["stream"].(bool)
	return ok && stream
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
	ctx := ex.r.Context()
	headerTimeout := time.Duration(0)
	idleTimeout := time.Duration(0)
	if ex.s != nil {
		headerTimeout = ex.s.cfg.UpstreamHeaderTimeout
		idleTimeout = ex.s.cfg.UpstreamIdleTimeout
	}
	if headerTimeout > 0 || idleTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		ex.upstreamCancel = cancel
	}
	if headerTimeout > 0 && ex.upstreamCancel != nil {
		timer := time.AfterFunc(headerTimeout, ex.upstreamCancel)
		// Stop the timer after Do returns. Do not cancel ctx on success:
		// the response body is bound to this child context.
		defer timer.Stop()
	}
	req, err := http.NewRequestWithContext(ctx, ex.r.Method, ex.target, bytes.NewReader(ex.rawReq))
	if err != nil {
		if ex.upstreamCancel != nil {
			ex.upstreamCancel()
		}
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
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{
		WroteRequest: func(httptrace.WroteRequestInfo) {
			ex.wroteRequest.Store(true)
		},
	}))
	UpstreamCallCount.Add(1)
	resp, err := ex.s.client.Do(req)
	if err != nil {
		if ex.upstreamCancel != nil {
			ex.upstreamCancel()
		}
		if ex.r != nil && ex.r.Context().Err() != nil {
			return nil, err
		}
		if headerTimeout > 0 && isContextTimeout(err) {
			return nil, headerTimeoutError(headerTimeout)
		}
		return nil, err
	}
	return resp, nil
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
	ex.noteUpstreamHeaders(resp.StatusCode)

	if ex.upstreamCancel != nil {
		defer ex.upstreamCancel()
	}
	idleTimeout := time.Duration(0)
	if ex.s != nil {
		idleTimeout = ex.s.cfg.UpstreamIdleTimeout
	}
	var idleTimer *time.Timer
	if idleTimeout > 0 && ex.upstreamCancel != nil {
		idleTimer = time.AfterFunc(idleTimeout, func() {
			ex.idleTimeoutFire.Store(true)
			ex.upstreamCancel()
		})
		defer idleTimer.Stop()
	}

	var rawResp bytes.Buffer
	headerSSE := strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream")
	encoding := strings.TrimSpace(resp.Header.Get("Content-Encoding"))
	compressed := encoding != "" && !strings.EqualFold(encoding, "identity")
	var recorder *sseRecorder
	if headerSSE && !compressed {
		recorder = newSSERecorder(ex.startedAt)
	}
	_, copyErr, copyWriteErr := copyAndFlushWithHooks(w, resp.Body, func() {
		if idleTimer != nil {
			idleTimer.Stop()
		}
	}, func(chunk []byte) {
		if idleTimer != nil {
			idleTimer.Reset(idleTimeout)
		}
		if firstByte == nil {
			now := time.Now().UTC()
			firstByte = &now
		}
		_, _ = rawResp.Write(chunk)
		if recorder != nil {
			recorder.Write(chunk)
		}
		ex.s.store.Progress(ex.session.ID, ex.callID, func(call *Call) {
			call.BytesReceived += int64(len(chunk))
			if call.FirstByteAt == nil && firstByte != nil {
				t := *firstByte
				call.FirstByteAt = &t
			}
		})
	})
	if ex.idleTimeoutFire.Load() && (ex.r == nil || ex.r.Context().Err() == nil) {
		copyErr = idleTimeoutError(idleTimeout)
	}
	ex.finishCall(resp, rawResp.Bytes(), firstByte, recorder, copyErr, copyWriteErr, headerSSE)
	return nil
}

func (ex *proxyExchange) finishCall(resp *http.Response, rawBody []byte, firstByte *time.Time, recorder *sseRecorder, copyErr error, copyWriteErr bool, headerSSE bool) {
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
		clientGone := ex.r != nil && ex.r.Context().Err() != nil
		idleTimeout := errors.Is(copyErr, ErrUpstreamIdleTimeout)
		if !idleTimeout && clientAbortedAfterHeaders(stream, complete, clientGone, copyWriteErr, copyErr) {
			// A complete SSE stream (terminal [DONE] or Responses
			// response.completed/failed/incomplete already written) is not
			// client_aborted when the SDK closes after that event.
			call.Normalized.ClientAborted = true
			if copyErr != nil {
				call.Normalized.ParserWarnings = append(call.Normalized.ParserWarnings, "response copy error: "+copyErr.Error())
			} else {
				call.Normalized.ParserWarnings = append(call.Normalized.ParserWarnings, "client canceled after response headers")
			}
		} else if copyErr != nil {
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
			target := ""
			if call.Normalized.Validation != nil {
				target = call.Normalized.Validation.Target
			}
			if call.APIFamily == "openai.responses" && ex.s.validation.linter != nil {
				extra = append(extra, ex.s.validation.linter.ConfirmStrictFallback(parseBody, omittedStrictToolIndexes(call.RawRequest))...)
			}
			vios, extra = applySpecTarget(target, vios, extra, parseBody)
			mergeResponseValidation(call, vios, extra, dur, trunc)
		}
		if call.Normalized.ClientAborted {
			call.ClientAborted = true
			call.AbortPhase = AbortPhaseAfterHeaders
			call.Normalized.FinishReason = "cancelled"
			if err := call.Normalized.Error; err != nil {
				if err.Normalized == "network" {
					call.Normalized.Error = nil
				} else {
					// After-headers cancel is not a stream HTTP/SSE error.
					err.MidStream = false
				}
			}
		}
		if idleTimeout {
			if stream && !complete {
				call.Normalized.StreamTruncated = true
			}
			call.Normalized.Error = &CallError{
				HTTPStatus: resp.StatusCode,
				Type:       "upstream_timeout",
				Code:       "upstream_idle_timeout",
				Message:    copyErr.Error(),
				Normalized: "upstream_idle_timeout",
				Source:     "proxy",
				MidStream:  false,
			}
			call.Normalized.FinishReason = "error"
		}
		logCall := snapshotAndCollapseViolations(call)
		endedCall = *call
		endedCall.Normalized.SpecViolations = logCall.Normalized.SpecViolations
	})
	var extra []any
	if endedCall.Normalized.ClientAborted {
		extra = []any{"client_aborted", true, "phase", AbortPhaseAfterHeaders}
	}
	ex.s.logProxyResponse(ex, endedCall, resp.StatusCode, extra...)
	ex.s.logValidationFindings(endedCall)
	if ex.span != nil {
		finishSpan(ex.span, endedCall, endedAt)
	}
}

// clientAbortedAfterHeaders reports whether a response that already had
// headers should be recorded as a client abort.
//
// A complete SSE stream is never aborted when the SDK closes after the
// terminal event. An incomplete stream (or a non-stream body that failed
// mid-write / was canceled) is aborted when the inbound context is gone
// or the copy failed writing to the client. Write errors must count even
// when Request.Context has not been canceled yet: net/http can surface
// the broken pipe before the request-context cancel is visible, and
// Finish would otherwise publish a settled record with client_aborted omitted.
func clientAbortedAfterHeaders(stream, complete, clientGone, copyWriteErr bool, copyErr error) bool {
	// Non-stream: a write failure or a context-canceled body read (doUpstream
	// binds the upstream request to the client context). Ordinary upstream
	// read errors stay network failures.
	incomplete := (stream && !complete) ||
		(!stream && (copyWriteErr || errors.Is(copyErr, context.Canceled)))
	if !incomplete {
		return false
	}
	return clientGone || copyWriteErr || errors.Is(copyErr, context.Canceled)
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

func (ex *proxyExchange) noteUpstreamHeaders(status int) {
	if ex == nil || ex.s == nil {
		return
	}
	ttfb := time.Since(ex.startedAt).Milliseconds()
	ex.s.store.Progress(ex.session.ID, ex.callID, func(call *Call) {
		call.HTTPStatus = status
	})
	ex.s.logger().Info("proxy upstream headers",
		"status", status,
		"call_id", ex.callID,
		"seq", ex.seq,
		"ttfb_ms", ttfb,
	)
}

func (s *Server) logProxyResponse(ex *proxyExchange, call Call, status any, extra ...any) {
	args := []any{
		"status", status,
		"call_id", ex.callID,
		"session", ex.session.ID,
		"seq", ex.seq,
		"api_family", call.APIFamily,
		"spec", specLogValue(call, s.specMode()),
	}
	args = append(args, extra...)
	s.logger().Info("proxy response", args...)
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
