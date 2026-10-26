// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	controlapi "github.com/barzoj-co/llmentary/api/control"
)

const controlDurationCap = 10 * time.Minute

// ControlRoutes is the control-API route table. Tests compare these paths to
// api/control/openapi.yaml.
var ControlRoutes = []struct {
	Method string
	Path   string
}{
	{http.MethodGet, "/healthz"},
	{http.MethodGet, "/openapi.yaml"},
	{http.MethodGet, "/config"},
	{http.MethodGet, "/version"},
	{http.MethodGet, "/sessions"},
	{http.MethodGet, "/sessions/"},
	{http.MethodGet, "/sessions/{id}"},
	{http.MethodGet, "/sessions/{id}/wait"},
	{http.MethodGet, "/sessions/{id}/otlp"},
	{http.MethodDelete, "/sessions/{id}"},
}

func (s *Server) ControlHandler() http.Handler {
	return s.controlAuth(http.HandlerFunc(s.serveControl))
}

func (s *Server) controlAuth(next http.Handler) http.Handler {
	if s.cfg.ControlToken == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+s.cfg.ControlToken {
			writeControlError(w, http.StatusUnauthorized, "unauthorized", "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) serveControl(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	switch {
	case path == "/healthz":
		s.serveHealthz(w, r)
	case path == "/openapi.yaml":
		s.serveOpenAPI(w, r)
	case path == "/config":
		s.serveConfig(w, r)
	case path == "/version":
		s.serveVersion(w, r)
	case path == "/sessions" || path == "/sessions/":
		s.serveSessionList(w, r)
	case strings.HasPrefix(path, "/sessions/"):
		s.serveSession(w, r)
	default:
		writeControlError(w, http.StatusNotFound, "not_found", "unknown control path; see GET /openapi.yaml")
	}
}

func (s *Server) serveOpenAPI(w http.ResponseWriter, r *http.Request) {
	if rejectControlMethod(w, r, http.MethodGet) {
		return
	}
	if rejectUnknownQuery(w, r) {
		return
	}
	w.Header().Set("Content-Type", "application/yaml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(StampOpenAPIVersion(controlapi.OpenAPIYAML, VersionString()))
}

func (s *Server) serveHealthz(w http.ResponseWriter, r *http.Request) {
	if rejectControlMethod(w, r, http.MethodGet) {
		return
	}
	if rejectUnknownQuery(w, r) {
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

func (s *Server) serveConfig(w http.ResponseWriter, r *http.Request) {
	if rejectControlMethod(w, r, http.MethodGet) {
		return
	}
	if rejectUnknownQuery(w, r) {
		return
	}
	writeJSON(w, s.configJSON())
}

func (s *Server) serveVersion(w http.ResponseWriter, r *http.Request) {
	if rejectControlMethod(w, r, http.MethodGet) {
		return
	}
	if rejectUnknownQuery(w, r) {
		return
	}
	writeJSON(w, versionJSON())
}

func (s *Server) serveSessionList(w http.ResponseWriter, r *http.Request) {
	if rejectControlMethod(w, r, http.MethodGet) {
		return
	}
	if rejectUnknownQuery(w, r, "source") {
		return
	}
	source := r.URL.Query().Get("source")
	if source != "" && !validSessionSource(source) {
		writeInvalidParam(w, "source", "unknown parameter value; allowed: default, header, baggage, path")
		return
	}
	sessions := s.store.List(source)
	if sessions == nil {
		sessions = []SessionSummary{}
	}
	writeJSON(w, map[string]any{"sessions": sessions})
}

func (s *Server) serveSession(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.EscapedPath(), "/sessions/")
	if rest == "" {
		s.serveSessionList(w, r)
		return
	}
	wait := false
	otlp := false
	switch {
	case strings.HasSuffix(rest, "/wait"):
		wait = true
		rest = strings.TrimSuffix(strings.TrimSuffix(rest, "/wait"), "/")
	case strings.HasSuffix(rest, "/otlp"):
		otlp = true
		rest = strings.TrimSuffix(strings.TrimSuffix(rest, "/otlp"), "/")
	}
	if rest == "" || strings.Contains(rest, "/") {
		writeControlError(w, http.StatusNotFound, "not_found", "unknown control path; see GET /openapi.yaml")
		return
	}
	sessionID, err := url.PathUnescape(rest)
	if err != nil || sessionID == "" {
		writeControlError(w, http.StatusBadRequest, "invalid_parameter", "session id is invalid")
		return
	}
	switch r.Method {
	case http.MethodGet:
		if wait {
			s.serveWait(w, r, sessionID)
			return
		}
		if otlp {
			if rejectUnknownQuery(w, r) {
				return
			}
			if _, ok := s.store.Lookup(sessionID); !ok {
				writeSessionNotFound(w, sessionID)
				return
			}
			writeJSON(w, s.tracing.Processor.OTLP(sessionID))
			return
		}
		if rejectUnknownQuery(w, r, "include") {
			return
		}
		include := r.URL.Query().Get("include")
		if include != "" && include != "in_flight" {
			writeInvalidParam(w, "include", "unknown parameter value; allowed: in_flight")
			return
		}
		if include == "in_flight" {
			session, inFlight, ok := s.store.SessionAndInFlight(sessionID)
			if !ok {
				writeSessionNotFound(w, sessionID)
				return
			}
			if inFlight == nil {
				inFlight = []InFlightCall{}
			}
			calls := session.Calls
			if calls == nil {
				calls = []Call{}
			}
			writeJSON(w, map[string]any{
				"id":        session.ID,
				"calls":     calls,
				"in_flight": inFlight,
			})
			return
		}
		session, ok := s.store.Lookup(sessionID)
		if !ok {
			writeSessionNotFound(w, sessionID)
			return
		}
		writeJSON(w, session)
	case http.MethodDelete:
		if wait || otlp {
			writeControlError(w, http.StatusNotFound, "not_found", "unknown control path; see GET /openapi.yaml")
			return
		}
		if rejectUnknownQuery(w, r) {
			return
		}
		s.store.Delete(sessionID)
		s.tracing.Processor.Delete(sessionID)
		w.WriteHeader(http.StatusNoContent)
	default:
		writeMethodNotAllowed(w, "GET, DELETE")
	}
}

func (s *Server) serveWait(w http.ResponseWriter, r *http.Request, sessionID string) {
	if rejectUnknownQuery(w, r, "timeout", "quiet", "min_calls", "exclude_cancelled") {
		return
	}
	q := r.URL.Query()
	quiet, err := parseControlDuration("quiet", defaultString(q.Get("quiet"), "500ms"))
	if err != nil {
		writeInvalidParam(w, "quiet", err.Error())
		return
	}
	timeout, err := parseControlDuration("timeout", defaultString(q.Get("timeout"), "30s"))
	if err != nil {
		writeInvalidParam(w, "timeout", err.Error())
		return
	}
	minCalls, err := parseMinCalls(q.Get("min_calls"))
	if err != nil {
		writeInvalidParam(w, "min_calls", err.Error())
		return
	}
	excludeCancelled, err := parseExcludeCancelled(q.Get("exclude_cancelled"))
	if err != nil {
		writeInvalidParam(w, "exclude_cancelled", err.Error())
		return
	}
	started := time.Now()
	session, err := s.store.WaitQuiet(r.Context(), sessionID, quiet, timeout, minCalls, excludeCancelled)
	if errors.Is(err, ErrSessionNotFound) {
		writeSessionNotFoundWait(w, sessionID, time.Since(started).Milliseconds())
		return
	}
	if errors.Is(err, ErrWaitTimeout) {
		session, inFlight, _ := s.store.SessionAndInFlight(sessionID)
		writeWaitTimeout(w, session, minCalls, inFlight, excludeCancelled)
		return
	}
	if err != nil {
		writeControlError(w, http.StatusGatewayTimeout, "wait_timeout", err.Error())
		return
	}
	writeJSON(w, session)
}

func validSessionSource(source string) bool {
	switch source {
	case "default", "header", "baggage", "path":
		return true
	default:
		return false
	}
}

func parseMinCalls(raw string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid integer")
	}
	if n < 0 {
		return 0, fmt.Errorf("must be >= 0")
	}
	return n, nil
}

func parseExcludeCancelled(raw string) (bool, error) {
	if raw == "" {
		return false, nil
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("invalid boolean")
	}
	return v, nil
}

func parseControlDuration(param, raw string) (time.Duration, error) {
	d, err := time.ParseDuration(raw)
	if err != nil {
		sec, ferr := strconv.ParseFloat(raw, 64)
		if ferr != nil {
			return 0, fmt.Errorf("invalid duration")
		}
		d = time.Duration(sec * float64(time.Second))
	}
	if d <= 0 || d > controlDurationCap {
		return 0, fmt.Errorf("must be > 0 and <= 10m")
	}
	return d, nil
}

func rejectControlMethod(w http.ResponseWriter, r *http.Request, allow string) bool {
	if r.Method == allow {
		return false
	}
	writeMethodNotAllowed(w, allow)
	return true
}

func rejectUnknownQuery(w http.ResponseWriter, r *http.Request, allowed ...string) bool {
	allow := map[string]struct{}{}
	for _, key := range allowed {
		allow[key] = struct{}{}
	}
	for key := range r.URL.Query() {
		if _, ok := allow[key]; !ok {
			msg := "unknown parameter; allowed: none"
			if len(allowed) > 0 {
				msg = "unknown parameter; allowed: " + strings.Join(allowed, ", ")
			}
			writeInvalidParam(w, key, msg)
			return true
		}
	}
	return false
}

func writeSessionNotFound(w http.ResponseWriter, id string) {
	writeJSONStatus(w, http.StatusNotFound, map[string]any{
		"error": map[string]any{
			"type":    "session_not_found",
			"message": fmt.Sprintf("no calls recorded for session %q; list sessions with GET /sessions", id),
		},
	})
}

func writeSessionNotFoundWait(w http.ResponseWriter, id string, waitedMS int64) {
	writeJSONStatus(w, http.StatusNotFound, map[string]any{
		"id": id,
		"error": map[string]any{
			"type":    "session_not_found",
			"message": fmt.Sprintf("no calls recorded for session %q; list sessions with GET /sessions", id),
		},
		"waited_ms": waitedMS,
	})
}

func writeWaitTimeout(w http.ResponseWriter, session Session, minCalls int, inFlight []InFlightCall, excludeCancelled bool) {
	if inFlight == nil {
		inFlight = []InFlightCall{}
	}
	calls := session.Calls
	if calls == nil {
		calls = []Call{}
	}
	errObj := map[string]any{
		"type":       "wait_timeout",
		"message":    "wait timed out",
		"calls_seen": len(calls),
		"min_calls":  minCalls,
		"active":     len(inFlight),
		"in_flight":  inFlight,
		"hint":       waitTimeoutHint(inFlight),
	}
	if excludeCancelled {
		errObj["exclude_cancelled"] = true
	}
	body := map[string]any{
		"id":    session.ID,
		"calls": calls,
		"error": errObj,
	}
	writeJSONStatus(w, http.StatusGatewayTimeout, body)
}

func waitTimeoutHint(inFlight []InFlightCall) string {
	if len(inFlight) == 0 {
		return "Raise LLMENTARY_WAIT_TIMEOUT if the app needs more time."
	}
	var headerMS, streamMS int64
	var hasHeader, hasStream bool
	for _, c := range inFlight {
		switch c.Phase {
		case InFlightPhaseAwaitingHeaders:
			hasHeader = true
			if c.ElapsedMS > headerMS {
				headerMS = c.ElapsedMS
			}
		case InFlightPhaseStreaming:
			hasStream = true
			if c.ElapsedMS > streamMS {
				streamMS = c.ElapsedMS
			}
		}
	}
	if hasHeader {
		return fmt.Sprintf("the upstream has not sent headers for %s; set LLMENTARY_UPSTREAM_HEADER_TIMEOUT to fail such calls fast", formatElapsedMS(headerMS))
	}
	if hasStream {
		return fmt.Sprintf("an upstream stream has been in progress for %s; set LLMENTARY_UPSTREAM_IDLE_TIMEOUT to fail stuck streams, or raise LLMENTARY_WAIT_TIMEOUT if the stream is still making progress", formatElapsedMS(streamMS))
	}
	return "Raise LLMENTARY_WAIT_TIMEOUT if the app needs more time."
}

func formatElapsedMS(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	return fmt.Sprintf("%.1fs", float64(ms)/1000.0)
}

func writeInvalidParam(w http.ResponseWriter, param, message string) {
	writeJSONStatus(w, http.StatusBadRequest, map[string]any{
		"error": map[string]any{
			"type":    "invalid_parameter",
			"param":   param,
			"message": message,
		},
	})
}

func writeMethodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]any{
		"error": map[string]any{
			"type":    "method_not_allowed",
			"message": "method not allowed",
		},
	})
}

func writeControlError(w http.ResponseWriter, status int, typ, message string) {
	writeJSONStatus(w, status, map[string]any{
		"error": map[string]any{
			"type":    typ,
			"message": message,
		},
	})
}

func writeJSONStatus(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
