// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"
)

const (
	DefaultSessionFrom     = "header,baggage,path,default"
	PropagateTraceOff      = "off"
	PropagateTraceAuto     = "auto"
	maxSessionIDBytes      = 256
	errTypeSessionRequired = "llmentary_session_required"
	errTypeUnknownPath     = "llmentary_unknown_path"
)

var defaultAllowPathPrefixes = []string{
	"v1", "openai", "api", "chat", "responses", "completions",
	"embeddings", "models", "files", "audio", "images",
	"moderations", "batches", "uploads", "vector_stores",
	"assistants", "threads", "realtime",
}

var allowedSessionSources = map[string]bool{
	"header":  true,
	"baggage": true,
	"path":    true,
	"default": true,
}

func ParseSessionFrom(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("--session-from is invalid; allowed values: header, baggage, path, default (env LLMENTARY_SESSION_FROM)")
	}
	var out []string
	seen := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.ToLower(strings.TrimSpace(part))
		if part == "" || !allowedSessionSources[part] {
			return nil, fmt.Errorf("--session-from=%q is invalid; allowed values: header, baggage, path, default (env LLMENTARY_SESSION_FROM)", raw)
		}
		if seen[part] {
			continue
		}
		seen[part] = true
		out = append(out, part)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("--session-from is invalid; allowed values: header, baggage, path, default (env LLMENTARY_SESSION_FROM)")
	}
	return out, nil
}

func ParseAllowPathPrefixes(raw string) []string {
	var out []string
	seen := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.Trim(strings.TrimSpace(part), "/")
		if part == "" || strings.Contains(part, "/") {
			continue
		}
		key := strings.ToLower(part)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, key)
	}
	return out
}

func validateSessionID(id string) error {
	if id == "" {
		return fmt.Errorf("session id is empty")
	}
	if len(id) > maxSessionIDBytes {
		return fmt.Errorf("session id exceeds %d bytes", maxSessionIDBytes)
	}
	if strings.Contains(id, "/") {
		return fmt.Errorf("session id cannot contain '/'")
	}
	for _, r := range id {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("session id contains a control character")
		}
	}
	if !utf8.ValidString(id) {
		return fmt.Errorf("session id is not valid UTF-8")
	}
	return nil
}

func splitSessionPrefix(requestPath, escapedPath string) (id, rest string, present bool, err error) {
	p := "/" + strings.TrimLeft(requestPath, "/")
	if p != "/s" && !strings.HasPrefix(p, "/s/") {
		return "", p, false, nil
	}
	if p == "/s" || p == "/s/" {
		return "", "/", true, fmt.Errorf("session id is empty")
	}
	esc := "/" + strings.TrimLeft(escapedPath, "/")
	escSeg := ""
	escapedFirst, escapedRest, _ := strings.Cut(strings.TrimPrefix(esc, "/"), "/")
	if first, firstErr := url.PathUnescape(escapedFirst); firstErr == nil && first == "s" {
		escSeg, _, _ = strings.Cut(escapedRest, "/")
		if strings.Contains(strings.ToLower(escSeg), "%2f") {
			return "", "/", true, fmt.Errorf("session id cannot contain '/'")
		}
	}
	after := strings.TrimPrefix(p, "/s/")
	_, more, found := strings.Cut(after, "/")
	if escSeg == "" {
		escSeg, _, _ = strings.Cut(after, "/")
	}
	decoded, decErr := url.PathUnescape(escSeg)
	if decErr != nil {
		return "", "/", true, fmt.Errorf("session id is invalid")
	}
	if err := validateSessionID(decoded); err != nil {
		return "", "/", true, err
	}
	if !found {
		return decoded, "/", true, nil
	}
	return decoded, "/" + more, true, nil
}

func stripNamedUpstreamPath(requestPath string) string {
	p := "/" + strings.TrimLeft(requestPath, "/")
	if !strings.HasPrefix(p, "/u/") {
		return p
	}
	after := strings.TrimPrefix(p, "/u/")
	_, more, found := strings.Cut(after, "/")
	if !found {
		return "/"
	}
	return "/" + more
}

func stripRoutingPrefixes(p string) string {
	clean := "/" + strings.TrimLeft(p, "/")
	for {
		next, ok := stripOneRoutingPrefix(clean)
		if !ok {
			return clean
		}
		clean = next
	}
}

func stripOneRoutingPrefix(p string) (string, bool) {
	p = "/" + strings.TrimLeft(p, "/")
	for _, prefix := range []string{"/u/", "/s/"} {
		if !strings.HasPrefix(p, prefix) {
			continue
		}
		rest := strings.TrimLeft(p[len(prefix):], "/")
		_, after, ok := strings.Cut(rest, "/")
		if !ok {
			return "/", true
		}
		return "/" + after, true
	}
	return p, false
}

func firstPathSegment(p string) string {
	p = strings.Trim(p, "/")
	if p == "" {
		return ""
	}
	seg, _, _ := strings.Cut(p, "/")
	return strings.ToLower(seg)
}

func (c Config) allowedWirePath(wirePath string) bool {
	seg := firstPathSegment(wirePath)
	if seg == "" {
		return false
	}
	for _, allow := range defaultAllowPathPrefixes {
		if seg == allow {
			return true
		}
	}
	for _, allow := range c.AllowPathPrefixes {
		if seg == strings.ToLower(strings.Trim(allow, "/")) {
			return true
		}
	}
	return false
}

func unknownPathHint(requestPath string) string {
	segs := pathSegments(requestPath)
	if len(segs) < 2 {
		return ""
	}
	second := strings.ToLower(segs[1])
	for _, allow := range defaultAllowPathPrefixes {
		if second == allow {
			return "/s/" + segs[0] + "/" + strings.Join(segs[1:], "/")
		}
	}
	return ""
}

func (s *Server) resolveSession(r *http.Request, pathID string, hasPath bool) (sessionInfo, error) {
	sources := s.cfg.SessionFrom
	if len(sources) == 0 {
		sources = []string{"header", "baggage", "path", "default"}
	}
	headerID := strings.TrimSpace(r.Header.Get(SessionHeaderName))
	baggageID := sessionIDFromBaggage(r.Header.Get("baggage"))
	for _, src := range sources {
		switch src {
		case "header":
			if headerID != "" {
				if err := validateSessionID(headerID); err != nil {
					return sessionInfo{}, err
				}
				return sessionInfo{ID: headerID, Source: "header"}, nil
			}
		case "baggage":
			if baggageID != "" {
				if err := validateSessionID(baggageID); err != nil {
					return sessionInfo{}, err
				}
				return sessionInfo{ID: baggageID, Source: "baggage"}, nil
			}
		case "path":
			if hasPath && pathID != "" {
				return sessionInfo{ID: pathID, Source: "path"}, nil
			}
		case "default":
			id := orDefault(s.cfg.DefaultSession, DefaultSessionID)
			return sessionInfo{ID: id, Source: "default"}, nil
		}
	}
	return sessionInfo{}, fmt.Errorf("session required; accepted sources: %s", strings.Join(sources, ", "))
}

func sessionIDFromBaggage(baggage string) string {
	for _, item := range strings.Split(baggage, ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(item), "=")
		if !ok {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "llmentary.session.id", "llmentary.session", "test-session", "x-test-session":
			if value = strings.TrimSpace(value); value != "" {
				return value
			}
		}
	}
	return ""
}

func writeLocalOpenAIError(w http.ResponseWriter, status int, typ, message string) {
	body := map[string]any{
		"error": map[string]any{
			"message": message,
			"type":    typ,
			"code":    typ,
		},
	}
	raw, _ := json.Marshal(body)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}
