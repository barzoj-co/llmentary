// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/barzoj-co/llmentary/internal/openaiapi"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

type Server struct {
	cfg         Config
	store       Store
	parser      Parser
	tracing     *TracingRuntime
	client      *http.Client
	specHandler http.Handler
	validation  *validationRuntime
}

func NewServer(cfg Config, store Store, parser Parser) (*Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if store == nil {
		store = NewMemoryStore()
	}
	if parser == nil {
		parser = TolerantParser{}
	}
	tracing, err := NewTracingRuntime()
	if err != nil {
		return nil, err
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	validation, err := newValidationRuntime(cfg)
	if err != nil {
		return nil, err
	}
	s := &Server{
		cfg:        cfg,
		store:      store,
		parser:     parser,
		tracing:    tracing,
		validation: validation,
		client: &http.Client{Transport: &http.Transport{
			Proxy:             http.ProxyFromEnvironment,
			DialContext:       (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			ForceAttemptHTTP2: true,
			MaxIdleConns:      100,
			IdleConnTimeout:   90 * time.Second,
		}},
	}
	handlers := &openAIHandlers{s: s}
	strict := openaiapi.NewStrictHandlerWithOptions(handlers, nil, openaiapi.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  s.handleRequestDecodeError,
		ResponseErrorHandlerFunc: s.handleResponseError,
	})
	s.specHandler = openaiapi.HandlerWithOptions(strict, openaiapi.StdHTTPServerOptions{})
	return s, nil
}

func (s *Server) logger() *slog.Logger {
	if s != nil && s.cfg.Logger != nil {
		return s.cfg.Logger
	}
	return slog.Default()
}

func (s *Server) configJSON() map[string]any {
	out := s.validation.configJSON()
	upstreams := map[string]string{}
	if s != nil {
		for name, u := range s.cfg.Upstreams {
			if u == nil {
				continue
			}
			upstreams[name] = sanitizeURL(u.Redacted())
		}
	}
	out["upstreams"] = upstreams
	return out
}

func (s *Server) ProxyHandler() http.Handler {
	return http.HandlerFunc(s.serveProxy)
}

func (s *Server) ControlHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/config", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, s.configJSON())
	})
	mux.HandleFunc("/sessions/", s.serveSession)
	return s.controlAuth(mux)
}

func (s *Server) controlAuth(next http.Handler) http.Handler {
	if s.cfg.ControlToken == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+s.cfg.ControlToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) ListenAndServe(ctx context.Context) error {
	errCh := make(chan error, 2)
	proxy := &http.Server{Addr: s.cfg.ListenAddr, Handler: s.ProxyHandler()}
	control := &http.Server{Addr: s.cfg.ControlAddr, Handler: s.ControlHandler()}
	go func() { errCh <- proxy.ListenAndServe() }()
	go func() { errCh <- control.ListenAndServe() }()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = proxy.Shutdown(shutdownCtx)
		_ = control.Shutdown(shutdownCtx)
	}()
	err := <-errCh
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func (s *Server) serveProxy(w http.ResponseWriter, r *http.Request) {
	upstreamName, restPath, targetBase, err := s.route(r.URL.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	session := sessionFromRequest(r)
	rawReq, err := drainAndRestore(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	target := cleanJoin(targetBase, restPath)
	target.RawQuery = r.URL.RawQuery
	apiVersion, deployment := extractAPIVersionAndDeployment(target)
	startedAt := time.Now().UTC()
	call := Call{
		StartedAt:     startedAt,
		Provider:      upstreamName,
		APIFamily:     detectAPIFamily(r.Method, target.Path),
		SessionSource: session.Source,
		Upstream: Upstream{
			Method:     r.Method,
			URL:        sanitizeURL(target.String()),
			APIVersion: apiVersion,
			Deployment: deployment,
		},
		Request:    Exchange{Headers: sanitizeHeaders(r.Header)},
		RequestIDs: map[string]string{},
		RawRequest: rawReq,
		RetryCount: parseRetryCount(r.Header),
	}
	seq, callID := s.store.Begin(session.ID, call)
	call.ID = callID
	call.Seq = seq
	call.SessionID = session.ID
	s.logger().Info("proxy request",
		"method", r.Method,
		"path", r.URL.Path,
		"session", session.ID,
		"call_id", callID,
		"upstream", call.Upstream.URL,
	)
	traceCtx := s.tracing.Propagator.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
	traceCtx, span := s.tracing.Tracer.Start(
		traceCtx,
		"chat "+requestModel(call),
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithTimestamp(startedAt),
		trace.WithAttributes(startSpanAttributes(call)...),
	)
	r = r.WithContext(traceCtx)
	ex := &proxyExchange{
		s:         s,
		r:         r,
		rawReq:    rawReq,
		session:   session,
		callID:    callID,
		startedAt: startedAt,
		span:      span,
		target:    target.String(),
	}

	if rewritten, ok := matchSpecOperation(r.Method, r.URL.Path); ok {
		rv := s.validation.checkRequest(call.APIFamily, r.Header.Get("Content-Type"), r.Header.Get("Content-Encoding"), rawReq)
		ex.requestVal = rv
		if s.validation != nil && s.validation.mode == "enforce" && rv.reject {
			s.rejectLocal(w, ex, rv)
			return
		}
		r2 := r.Clone(withExchange(traceCtx, ex))
		r2.URL.Path = rewritten
		r2.Body = io.NopCloser(bytes.NewReader(rawReq))
		s.specHandler.ServeHTTP(w, r2)
		return
	}
	s.passthrough(w, ex)
}

func (s *Server) route(requestPath string) (string, string, *url.URL, error) {
	clean := strings.TrimLeft(requestPath, "/")
	if strings.HasPrefix(clean, "u/") {
		parts := strings.SplitN(clean, "/", 3)
		if len(parts) < 2 {
			return "", "", nil, fmt.Errorf("missing upstream name")
		}
		base, ok := s.cfg.Upstreams[parts[1]]
		if !ok {
			return "", "", nil, fmt.Errorf("unknown upstream %q", parts[1])
		}
		rest := "/"
		if len(parts) == 3 {
			rest = "/" + parts[2]
		}
		return parts[1], rest, base, nil
	}
	if len(s.cfg.Upstreams) == 1 {
		for name, base := range s.cfg.Upstreams {
			return name, requestPath, base, nil
		}
	}
	if base, ok := s.cfg.Upstreams["default"]; ok {
		return "default", requestPath, base, nil
	}
	return "", "", nil, fmt.Errorf("bare proxy path requires exactly one upstream or a default upstream")
}

func (s *Server) serveSession(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/sessions/")
	if rest == "" {
		http.NotFound(w, r)
		return
	}
	wait := false
	otlp := false
	if strings.HasSuffix(rest, "/wait") {
		wait = true
		rest = strings.TrimSuffix(rest, "/wait")
		rest = strings.TrimSuffix(rest, "/")
	} else if strings.HasSuffix(rest, "/otlp") {
		otlp = true
		rest = strings.TrimSuffix(rest, "/otlp")
		rest = strings.TrimSuffix(rest, "/")
	}
	sessionID, err := url.PathUnescape(rest)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodGet:
		if otlp {
			writeJSON(w, s.tracing.Processor.OTLP(sessionID))
			return
		}
		var session Session
		if wait {
			quiet, _ := time.ParseDuration(defaultString(r.URL.Query().Get("quiet"), "500ms"))
			timeout, _ := time.ParseDuration(defaultString(r.URL.Query().Get("timeout"), "30s"))
			session, err = s.store.WaitQuiet(r.Context(), sessionID, quiet, timeout)
			if err != nil {
				http.Error(w, err.Error(), http.StatusGatewayTimeout)
				return
			}
		} else {
			session = s.store.Session(sessionID)
		}
		writeJSON(w, session)
	case http.MethodDelete:
		if wait || otlp {
			http.NotFound(w, r)
			return
		}
		s.store.Delete(sessionID)
		s.tracing.Processor.Delete(sessionID)
		w.WriteHeader(http.StatusNoContent)
	default:
		w.Header().Set("Allow", "GET, DELETE")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) finishWithProxyError(sessionID, callID string, startedAt time.Time, err error, status int, rv requestValidation) Call {
	endedAt := time.Now().UTC()
	var endedCall Call
	s.store.Finish(sessionID, callID, func(call *Call) {
		call.EndedAt = endedAt
		call.LatencyMS = endedAt.Sub(startedAt).Milliseconds()
		call.HTTPStatus = status
		call.Normalized.Error = &CallError{HTTPStatus: status, Message: err.Error(), Normalized: "network"}
		call.Normalized.FinishReason = "error"
		s.parser.Parse(call)
		applyRequestValidation(call, rv)
		endedCall = *call
	})
	s.logValidationFindings(endedCall)
	return endedCall
}

func copyAndFlush(w http.ResponseWriter, r io.Reader, onChunk func([]byte)) (int64, error) {
	buf := make([]byte, 32*1024)
	var written int64
	flusher, _ := w.(http.Flusher)
	for {
		nr, er := r.Read(buf)
		if nr > 0 {
			chunk := append([]byte(nil), buf[:nr]...)
			if onChunk != nil {
				onChunk(chunk)
			}
			nw, ew := w.Write(chunk)
			written += int64(nw)
			if flusher != nil {
				flusher.Flush()
			}
			if ew != nil {
				return written, ew
			}
			if nr != nw {
				return written, io.ErrShortWrite
			}
		}
		if er != nil {
			if er == io.EOF {
				return written, nil
			}
			return written, er
		}
	}
}

func removeHopByHopHeaders(h http.Header) {
	for _, key := range []string{"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade"} {
		h.Del(key)
	}
}

func isSSE(headers http.Header, body []byte) bool {
	if strings.Contains(strings.ToLower(headers.Get("Content-Type")), "text/event-stream") {
		return true
	}
	return bytes.HasPrefix(bytes.TrimSpace(body), []byte("data:")) || bytes.Contains(body, []byte("\nevent:"))
}

func retryAfter(h http.Header) string {
	if value := h.Get("retry-after-ms"); value != "" {
		return value
	}
	return h.Get("retry-after")
}

func recordRequestIDs(call *Call, h http.Header) {
	for key, values := range h {
		lower := strings.ToLower(key)
		if (strings.HasPrefix(lower, "x-") || strings.HasPrefix(lower, "apim-") || lower == "request-id") && len(values) > 0 {
			call.RequestIDs[lower] = values[0]
		}
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
