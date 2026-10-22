// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/barzoj-co/llmentary/internal/openaiapi"
)

const derivedVersion = 3

type Parser interface {
	Parse(call *Call)
}

type TolerantParser struct{}

func (p TolerantParser) Parse(call *Call) {
	call.Normalized.DerivedVersion = derivedVersion
	call.Normalized.PromptChars = len(call.RawRequest)
	switch call.APIFamily {
	case "openai.chat":
		parseChat(call)
	case "openai.responses":
		parseResponses(call)
	default:
		if call.Normalized.Error != nil {
			call.Normalized.FinishReason = "error"
		} else {
			call.Normalized.FinishReason = "unknown"
		}
	}
}

func normalizeJSONBody(raw []byte) ([]byte, string) {
	first, rest, err := splitFirstJSON(raw)
	if err != nil {
		return raw, ""
	}
	if i := indexNonWS(rest); i >= 0 {
		offset := len(raw) - len(rest) + i
		return first, fmt.Sprintf("trailing_data_after_json at offset %d", offset)
	}
	return first, ""
}

func detectAPIFamily(method, requestPath string) string {
	if method != http.MethodPost {
		return "passthrough"
	}
	clean := strings.TrimRight(requestPath, "/")
	switch {
	case strings.HasSuffix(clean, "/chat/completions"):
		return "openai.chat"
	case strings.HasSuffix(clean, "/responses"):
		return "openai.responses"
	default:
		return "passthrough"
	}
}

func parseRetryCount(header http.Header) *int {
	value := header.Get("x-stainless-retry-count")
	if value == "" {
		return nil
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return nil
	}
	return &n
}

type sessionInfo struct {
	ID     string
	Source string
}

func sessionFromRequest(r *http.Request) sessionInfo {
	if id := strings.TrimSpace(r.Header.Get(SessionHeaderName)); id != "" {
		return sessionInfo{ID: id, Source: "header"}
	}
	for _, item := range strings.Split(r.Header.Get("baggage"), ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(item), "=")
		if !ok {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "llmentary.session.id", "llmentary.session", "test-session", "x-test-session":
			if value = strings.TrimSpace(value); value != "" {
				return sessionInfo{ID: value, Source: "baggage"}
			}
		}
	}
	return sessionInfo{ID: DefaultSessionID, Source: "default"}
}

func stripLlmentaryBaggage(header http.Header) {
	baggage := header.Get("baggage")
	if baggage == "" {
		return
	}
	var kept []string
	for _, item := range strings.Split(baggage, ",") {
		item = strings.TrimSpace(item)
		key, _, _ := strings.Cut(item, "=")
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(key)), "llmentary.") {
			continue
		}
		if item != "" {
			kept = append(kept, item)
		}
	}
	if len(kept) == 0 {
		header.Del("baggage")
		return
	}
	header.Set("baggage", strings.Join(kept, ", "))
}

func sanitizeHeaders(h http.Header) map[string][]string {
	out := make(map[string][]string, len(h))
	for key, values := range h {
		lower := strings.ToLower(key)
		safe := make([]string, len(values))
		for i, value := range values {
			if isSecretHeader(lower) {
				safe[i] = hashSecret(value)
			} else {
				safe[i] = value
			}
		}
		out[key] = safe
	}
	return out
}

func isSecretHeader(key string) bool {
	switch key {
	case "authorization", "api-key", "x-api-key", "x-goog-api-key", "cookie", "set-cookie":
		return true
	default:
		return false
	}
}

func sanitizeURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	q := u.Query()
	for _, key := range []string{"key", "api_key", "sig", "signature"} {
		for _, value := range q[key] {
			if value != "" {
				q.Set(key, hashSecret(value))
			}
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func sanitizeInFlightURL(raw string) string {
	s := sanitizeURL(raw)
	u, err := url.Parse(s)
	if err != nil {
		return s
	}
	u.User = nil
	u.RawQuery = ""
	return u.String()
}

func hashSecret(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])[:16]
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func extractAPIVersionAndDeployment(u *url.URL) (string, string) {
	apiVersion := u.Query().Get("api-version")
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i := 0; i < len(parts)-1; i++ {
		if parts[i] == "deployments" {
			return apiVersion, parts[i+1]
		}
	}
	return apiVersion, ""
}

func parseSSE(raw []byte, startedAt time.Time) ([]SSEEvent, bool, []string) {
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	var (
		eventName string
		dataLines [][]byte
		events    []SSEEvent
		complete  bool
		warnings  []string
	)
	flush := func(markComplete bool) {
		if eventName == "" && len(dataLines) == 0 {
			return
		}
		data := bytes.Join(dataLines, []byte("\n"))
		evt := SSEEvent{
			Event: eventName,
			Data:  append([]byte(nil), data...),
			AtMS:  time.Since(startedAt).Milliseconds(),
		}
		events = append(events, evt)
		if markComplete && sseTerminalSeen(eventName, data) {
			complete = true
		}
		eventName = ""
		dataLines = nil
	}
	for scanner.Scan() {
		line := scanner.Bytes()
		line = bytes.TrimSuffix(line, []byte("\r"))
		if len(line) == 0 {
			flush(true)
			continue
		}
		if bytes.HasPrefix(line, []byte(":")) {
			events = append(events, SSEEvent{Comment: strings.TrimSpace(string(line[1:])), AtMS: time.Since(startedAt).Milliseconds()})
			continue
		}
		field, value, ok := bytes.Cut(line, []byte(":"))
		if !ok {
			continue
		}
		if len(value) > 0 && value[0] == ' ' {
			value = value[1:]
		}
		switch string(field) {
		case "event":
			eventName = string(value)
		case "data":
			dataLines = append(dataLines, append([]byte(nil), value...))
		}
	}
	if len(dataLines) > 0 || eventName != "" {
		flush(false)
	}
	if err := scanner.Err(); err != nil {
		warnings = append(warnings, "sse scan error: "+err.Error())
	}
	return events, complete, warnings
}

type sseRecorder struct {
	startedAt time.Time
	pending   []byte
	events    []SSEEvent
	complete  bool
	warnings  []string
}

func newSSERecorder(startedAt time.Time) *sseRecorder {
	return &sseRecorder{startedAt: startedAt}
}

func (r *sseRecorder) Write(chunk []byte) {
	r.pending = append(r.pending, chunk...)
	for {
		idx, delimLen := sseDelimiter(r.pending)
		if idx < 0 {
			return
		}
		block := append([]byte(nil), r.pending[:idx+delimLen]...)
		r.pending = append([]byte(nil), r.pending[idx+delimLen:]...)
		r.recordBlock(block, true)
	}
}

func (r *sseRecorder) Finish() ([]SSEEvent, bool, []string) {
	if len(bytes.TrimSpace(r.pending)) > 0 {
		r.recordBlock(r.pending, false)
		r.pending = nil
	}
	return r.events, r.complete, r.warnings
}

func (r *sseRecorder) recordBlock(block []byte, allowTerminal bool) {
	events, complete, warnings := parseSSE(block, r.startedAt)
	atMS := time.Since(r.startedAt).Milliseconds()
	for _, event := range events {
		event.AtMS = atMS
		r.events = append(r.events, event)
	}
	if complete && allowTerminal {
		r.complete = true
	}
	r.warnings = append(r.warnings, warnings...)
}

func sseTerminalSeen(eventName string, data []byte) bool {
	if bytes.Equal(bytes.TrimSpace(data), []byte("[DONE]")) {
		return true
	}
	name := eventName
	if name == "" {
		var probe struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(data, &probe) == nil {
			name = probe.Type
		}
	}
	switch name {
	case "response.completed", "response.failed", "response.incomplete":
		return true
	default:
		return false
	}
}

func sseDelimiter(b []byte) (int, int) {
	lf := bytes.Index(b, []byte("\n\n"))
	crlf := bytes.Index(b, []byte("\r\n\r\n"))
	switch {
	case lf < 0 && crlf < 0:
		return -1, 0
	case lf < 0:
		return crlf, 4
	case crlf < 0:
		return lf, 2
	case crlf < lf:
		return crlf, 4
	default:
		return lf, 2
	}
}

func systemHash(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	return hashBytes([]byte(text))
}

func schemaHash(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var v any
	if err := json.Unmarshal(raw, &v); err == nil {
		if b, err := json.Marshal(v); err == nil {
			return hashBytes(b)
		}
	}
	return hashBytes(raw)
}

func parseJSONArguments(raw string) (json.RawMessage, string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, ""
	}
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return nil, err.Error()
	}
	b, _ := json.Marshal(v)
	return b, ""
}

func sortToolCalls(calls []ToolCall) {
	sort.SliceStable(calls, func(i, j int) bool {
		return calls[i].Index < calls[j].Index
	})
}

func contentString(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var parts []struct {
		Type    string          `json:"type"`
		Text    string          `json:"text"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &parts); err == nil {
		var b strings.Builder
		for _, part := range parts {
			if part.Text != "" {
				b.WriteString(part.Text)
			} else if text := contentString(part.Content); text != "" {
				b.WriteString(text)
			}
		}
		return b.String()
	}
	return string(raw)
}

func usageFromChat(raw json.RawMessage, source string) *Usage {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	var u openaiapi.CompletionUsage
	if err := json.Unmarshal(raw, &u); err != nil {
		return nil
	}
	usage := &Usage{Source: source, Raw: append(json.RawMessage(nil), raw...)}
	if hasJSONKey(raw, "prompt_tokens") {
		usage.InputTokens = intPtr(u.PromptTokens)
	}
	if hasJSONKey(raw, "completion_tokens") {
		usage.OutputTokens = intPtr(u.CompletionTokens)
	}
	if hasJSONKey(raw, "total_tokens") {
		usage.TotalTokens = intPtr(u.TotalTokens)
	}
	if u.PromptTokensDetails != nil {
		if hasNestedJSONKey(raw, "prompt_tokens_details", "cached_tokens") && u.PromptTokensDetails.CachedTokens != nil {
			usage.CachedInputTokens = intPtr(*u.PromptTokensDetails.CachedTokens)
		}
		if hasNestedJSONKey(raw, "prompt_tokens_details", "cache_write_tokens") && u.PromptTokensDetails.CacheWriteTokens != nil {
			usage.CacheWriteTokens = intPtr(*u.PromptTokensDetails.CacheWriteTokens)
		}
		if hasNestedJSONKey(raw, "prompt_tokens_details", "audio_tokens") && u.PromptTokensDetails.AudioTokens != nil {
			usage.AudioTokens = intPtr(*u.PromptTokensDetails.AudioTokens)
		}
	}
	if u.CompletionTokensDetails != nil {
		if hasNestedJSONKey(raw, "completion_tokens_details", "reasoning_tokens") && u.CompletionTokensDetails.ReasoningTokens != nil {
			usage.ReasoningTokens = intPtr(*u.CompletionTokensDetails.ReasoningTokens)
		}
		if hasNestedJSONKey(raw, "completion_tokens_details", "accepted_prediction_tokens") && u.CompletionTokensDetails.AcceptedPredictionTokens != nil {
			usage.AcceptedPredictionTokens = intPtr(*u.CompletionTokensDetails.AcceptedPredictionTokens)
		}
		if hasNestedJSONKey(raw, "completion_tokens_details", "rejected_prediction_tokens") && u.CompletionTokensDetails.RejectedPredictionTokens != nil {
			usage.RejectedPredictionTokens = intPtr(*u.CompletionTokensDetails.RejectedPredictionTokens)
		}
		if usage.AudioTokens == nil && hasNestedJSONKey(raw, "completion_tokens_details", "audio_tokens") && u.CompletionTokensDetails.AudioTokens != nil {
			usage.AudioTokens = intPtr(*u.CompletionTokensDetails.AudioTokens)
		}
	}
	applyUsageExtensions(usage, raw)
	return usage
}

func usageFromResponses(raw json.RawMessage, source string) *Usage {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	var u openaiapi.ResponseUsage
	if err := json.Unmarshal(raw, &u); err != nil {
		return nil
	}
	usage := &Usage{Source: source, Raw: append(json.RawMessage(nil), raw...)}
	if hasJSONKey(raw, "input_tokens") {
		usage.InputTokens = intPtr(u.InputTokens)
	}
	if hasJSONKey(raw, "output_tokens") {
		usage.OutputTokens = intPtr(u.OutputTokens)
	}
	if hasJSONKey(raw, "total_tokens") {
		usage.TotalTokens = intPtr(u.TotalTokens)
	}
	if hasNestedJSONKey(raw, "input_tokens_details", "cached_tokens") {
		usage.CachedInputTokens = intPtr(u.InputTokensDetails.CachedTokens)
	}
	if hasNestedJSONKey(raw, "input_tokens_details", "cache_write_tokens") {
		usage.CacheWriteTokens = intPtr(u.InputTokensDetails.CacheWriteTokens)
	}
	if hasNestedJSONKey(raw, "output_tokens_details", "reasoning_tokens") {
		usage.ReasoningTokens = intPtr(u.OutputTokensDetails.ReasoningTokens)
	}
	applyUsageExtensions(usage, raw)
	return usage
}

func jsonField(raw json.RawMessage, key string) json.RawMessage {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	return m[key]
}

func hasJSONKey(raw json.RawMessage, key string) bool {
	var m map[string]json.RawMessage
	return json.Unmarshal(raw, &m) == nil && m[key] != nil
}

func hasNestedJSONKey(raw json.RawMessage, keys ...string) bool {
	current := raw
	for _, key := range keys {
		var m map[string]json.RawMessage
		if json.Unmarshal(current, &m) != nil {
			return false
		}
		current = m[key]
		if current == nil {
			return false
		}
	}
	return true
}

func intPtr(v int) *int {
	return &v
}

func midStreamError(status int, raw json.RawMessage) *CallError {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	var holder struct {
		Type    string          `json:"type"`
		Code    *string         `json:"code"`
		Message string          `json:"message"`
		Error   json.RawMessage `json:"error"`
	}
	_ = json.Unmarshal(raw, &holder)

	// Spec Responses stream error: top-level type/code/message (ResponseErrorEvent).
	var ev openaiapi.ResponseErrorEvent
	if json.Unmarshal(raw, &ev) == nil && (ev.Type == "error" || holder.Type == "error") && ev.Message != "" {
		code := ""
		if ev.Code != nil {
			code = *ev.Code
		}
		return &CallError{
			HTTPStatus: status,
			Type:       string(ev.Type),
			Code:       code,
			Message:    ev.Message,
			Normalized: normalizeError(status, code, string(ev.Type)),
			MidStream:  true,
			Source:     "upstream",
		}
	}

	if len(holder.Error) == 0 || bytes.Equal(bytes.TrimSpace(holder.Error), []byte("null")) {
		return nil
	}
	err := &CallError{HTTPStatus: status, MidStream: true, Source: "upstream"}
	if typ, code, message, ok := parseLooseError(holder.Error); ok {
		err.Type = typ
		err.Code = code
		err.Message = message
	} else {
		var asString string
		if json.Unmarshal(holder.Error, &asString) == nil {
			err.Message = asString
		} else {
			return nil
		}
	}
	err.Normalized = normalizeError(status, err.Code, err.Type)
	return err
}

func parseLooseError(raw json.RawMessage) (typ, code, message string, ok bool) {
	var fallback struct {
		Type    string          `json:"type"`
		Code    json.RawMessage `json:"code"`
		Message string          `json:"message"`
	}
	if json.Unmarshal(raw, &fallback) != nil {
		return "", "", "", false
	}
	if fallback.Message == "" && fallback.Type == "" {
		return "", "", "", false
	}
	return fallback.Type, looseErrorCode(fallback.Code), fallback.Message, true
}

func looseErrorCode(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		return n.String()
	}
	return string(raw)
}

func normalizeError(status int, code, typ string) string {
	text := strings.ToLower(strings.TrimSpace(code + " " + typ))
	compact := strings.NewReplacer("_", "", "-", "", " ", "").Replace(text)
	n := errorCodeNumber(code)
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return "auth"
	case status == http.StatusTooManyRequests || n == http.StatusTooManyRequests || strings.Contains(text, "rate") || strings.Contains(text, "capacity"):
		return "rate_limit"
	case status >= 500 || n >= 500 || strings.Contains(text, "overloaded") || strings.Contains(compact, "servererror") || strings.Contains(compact, "internalserver"):
		return "server"
	case strings.Contains(text, "content_filter"):
		return "content_filter"
	case status >= 400:
		return "bad_request"
	default:
		return "unknown"
	}
}

func errorCodeNumber(code string) int {
	code = strings.TrimSpace(code)
	if code == "" {
		return 0
	}
	n, err := strconv.Atoi(code)
	if err != nil {
		return 0
	}
	return n
}

func errorFromHTTP(status int, raw []byte) *CallError {
	if status < 400 {
		return nil
	}
	err := &CallError{HTTPStatus: status, Normalized: normalizeError(status, "", ""), Source: "upstream"}
	var wrapper openaiapi.ErrorResponse
	if json.Unmarshal(raw, &wrapper) == nil && (wrapper.Error.Message != "" || wrapper.Error.Type != "") {
		err.Type = wrapper.Error.Type
		if wrapper.Error.Code != nil {
			err.Code = *wrapper.Error.Code
		}
		err.Message = wrapper.Error.Message
		err.Normalized = normalizeError(status, err.Code, err.Type)
		return err
	}
	var loose struct {
		Error struct {
			Type    string `json:"type"`
			Code    any    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &loose) == nil && (loose.Error.Message != "" || loose.Error.Type != "") {
		err.Type = loose.Error.Type
		err.Code = fmt.Sprint(loose.Error.Code)
		if loose.Error.Code == nil {
			err.Code = ""
		}
		err.Message = loose.Error.Message
		err.Normalized = normalizeError(status, err.Code, err.Type)
	}
	return err
}

// JoinUpstreamURL joins an upstream base URL with a request path, overlapping
// trailing base segments with a matching request-path prefix so
// /v1 + /v1/chat/completions becomes /v1/chat/completions, not /v1/v1/....
func JoinUpstreamURL(base *url.URL, rest string) *url.URL {
	return cleanJoin(base, rest)
}

func cleanJoin(base *url.URL, rest string) *url.URL {
	u := *base
	trailingSlash := strings.HasSuffix(rest, "/") && rest != "/"
	joined := overlapJoin(base.Path, rest)
	if trailingSlash && !strings.HasSuffix(joined, "/") {
		joined += "/"
	}
	u.Path = joined
	return &u
}

var (
	versionSegmentRE  = regexp.MustCompile(`(?i)^v\d+(alpha|beta)?\d*$`)
	versionJoinWarned atomic.Bool
	versionJoinWarn   = func(basePath, restPath, joined string) {
		slog.Default().Warn("dropped client version segment; upstream base already has a version path",
			"upstream_path", basePath,
			"request_path", restPath,
			"joined_path", joined,
		)
	}
)

func isVersionSegment(s string) bool {
	return versionSegmentRE.MatchString(s)
}

func pathHasVersionSegment(segs []string) bool {
	for _, s := range segs {
		if isVersionSegment(s) {
			return true
		}
	}
	return false
}

func overlapJoin(basePath, restPath string) string {
	baseSegs := pathSegments(basePath)
	restSegs := pathSegments(restPath)
	overlap := 0
	maxK := len(baseSegs)
	if len(restSegs) < maxK {
		maxK = len(restSegs)
	}
	for k := maxK; k >= 1; k-- {
		if pathSegmentsEqual(baseSegs[len(baseSegs)-k:], restSegs[:k]) {
			overlap = k
			break
		}
	}
	restAfter := restSegs[overlap:]
	rewrote := false
	if overlap == 0 && len(restAfter) > 0 && isVersionSegment(restAfter[0]) && pathHasVersionSegment(baseSegs) {
		restAfter = restAfter[1:]
		rewrote = true
	}
	joined := append(append([]string{}, baseSegs...), restAfter...)
	out := "/"
	if len(joined) > 0 {
		out = path.Clean("/" + strings.Join(joined, "/"))
	}
	if rewrote && versionJoinWarned.CompareAndSwap(false, true) {
		versionJoinWarn(basePath, restPath, out)
	}
	return out
}

func pathSegments(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

func pathSegmentsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func cloneHeader(h http.Header) http.Header {
	out := make(http.Header, len(h))
	for k, values := range h {
		out[k] = append([]string(nil), values...)
	}
	return out
}
