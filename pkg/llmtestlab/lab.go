// SPDX-License-Identifier: MIT

package llmtestlab

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

const (
	defaultProxyURL   = "http://127.0.0.1:7777"
	defaultControlURL = "http://127.0.0.1:7778"
)

type Lab struct {
	t          testing.TB
	client     *Client
	sessionID  string
	proxyURL   string
	quiet      time.Duration
	timeout    time.Duration
	httpClient *http.Client
}

type LabOption func(*labConfig)

type labConfig struct {
	controlURL   string
	proxyURL     string
	controlToken string
	httpClient   *http.Client
	quiet        time.Duration
	timeout      time.Duration
}

func New(t testing.TB, opts ...LabOption) *Lab {
	t.Helper()
	cfg := labConfig{
		controlURL: getenvDefault("LLMTESTLAB_CONTROL_URL", defaultControlURL),
		proxyURL:   getenvDefault("LLMTESTLAB_PROXY_URL", defaultProxyURL),
		quiet:      500 * time.Millisecond,
		timeout:    30 * time.Second,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	clientOpts := []ClientOption{}
	if cfg.httpClient != nil {
		clientOpts = append(clientOpts, WithHTTPClient(cfg.httpClient))
	}
	if cfg.controlToken != "" {
		clientOpts = append(clientOpts, WithControlToken(cfg.controlToken))
	}
	client, err := NewClient(cfg.controlURL, clientOpts...)
	if err != nil {
		t.Fatalf("llmtestlab: create client: %v", err)
	}
	lab := &Lab{
		t:          t,
		client:     client,
		sessionID:  client.NewSession(),
		proxyURL:   strings.TrimRight(cfg.proxyURL, "/"),
		quiet:      cfg.quiet,
		timeout:    cfg.timeout,
		httpClient: cfg.httpClient,
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = lab.client.Delete(ctx, lab.sessionID)
	})
	return lab
}

func WithControlURL(rawURL string) LabOption {
	return func(c *labConfig) { c.controlURL = rawURL }
}

func WithProxyURL(rawURL string) LabOption {
	return func(c *labConfig) { c.proxyURL = rawURL }
}

func WithLabControlToken(token string) LabOption {
	return func(c *labConfig) { c.controlToken = token }
}

func WithLabHTTPClient(client *http.Client) LabOption {
	return func(c *labConfig) { c.httpClient = client }
}

func WithWait(quiet, timeout time.Duration) LabOption {
	return func(c *labConfig) {
		c.quiet = quiet
		c.timeout = timeout
	}
}

func (l *Lab) SessionID() string { return l.sessionID }

func (l *Lab) Header() (string, string) { return Header(l.sessionID) }

func (l *Lab) Headers() http.Header {
	h := http.Header{}
	key, value := l.Header()
	h.Set(key, value)
	return h
}

func (l *Lab) Baggage() string {
	return "llmtestlab.session.id=" + url.QueryEscape(l.sessionID)
}

func (l *Lab) BaseURL(upstream string) string {
	if upstream == "" {
		return l.proxyURL
	}
	return l.proxyURL + "/u/" + strings.Trim(upstream, "/")
}

func (l *Lab) OpenAIBaseURL(upstream string) string {
	return l.BaseURL(upstream)
}

func (l *Lab) HTTPClient() *http.Client {
	client := http.Client{}
	if l.httpClient != nil {
		client = *l.httpClient
	}
	transport := http.DefaultTransport
	if client.Transport != nil {
		transport = client.Transport
	}
	client.Transport = sessionTransport{sessionID: l.sessionID, base: transport}
	return &client
}

type sessionTransport struct {
	sessionID string
	base      http.RoundTripper
}

func (t sessionTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header = req.Header.Clone()
	clone.Header.Set(SessionHeaderName, t.sessionID)
	return t.base.RoundTrip(clone)
}

func (l *Lab) Wait(ctx context.Context) Trace {
	l.t.Helper()
	calls, err := l.client.Wait(ctx, l.sessionID, l.quiet, l.timeout)
	if err != nil {
		l.t.Fatalf("llmtestlab: wait for session %s: %v", l.sessionID, err)
	}
	return Trace{SessionID: l.sessionID, Calls: calls}
}

func (l *Lab) Calls(ctx context.Context) Trace {
	l.t.Helper()
	calls, err := l.client.Calls(ctx, l.sessionID)
	if err != nil {
		l.t.Fatalf("llmtestlab: fetch session %s: %v", l.sessionID, err)
	}
	return Trace{SessionID: l.sessionID, Calls: calls}
}

func (l *Lab) Expect(ctx context.Context) *Assertions {
	l.t.Helper()
	trace := l.Wait(ctx)
	return &Assertions{t: l.t, trace: trace, fatal: true}
}

func (l *Lab) Check(ctx context.Context) *Assertions {
	l.t.Helper()
	trace := l.Wait(ctx)
	return &Assertions{t: l.t, trace: trace}
}

type Trace struct {
	SessionID string
	Calls     []Call
}

func (tr Trace) Query() Query { return Query{trace: tr, calls: append([]Call(nil), tr.Calls...)} }

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
			fmt.Fprintf(&b, "    tool[%d] %s args=%s\n", tool.Index, tool.Name, compactJSON(tool.ArgumentsRaw))
		}
		if call.Normalized.Error != nil {
			fmt.Fprintf(&b, "    error type=%s code=%s message=%s mid_stream=%v\n", call.Normalized.Error.Type, call.Normalized.Error.Code, call.Normalized.Error.Message, call.Normalized.Error.MidStream)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

type Query struct {
	trace Trace
	calls []Call
}

func (q Query) Calls() []Call { return append([]Call(nil), q.calls...) }
func (q Query) Len() int      { return len(q.calls) }
func (q Query) Trace() Trace  { return Trace{SessionID: q.trace.SessionID, Calls: q.Calls()} }

func (q Query) Expect(t testing.TB) *Assertions {
	t.Helper()
	return &Assertions{t: t, trace: q.Trace(), fatal: true}
}

func (q Query) Check(t testing.TB) *Assertions {
	t.Helper()
	return &Assertions{t: t, trace: q.Trace()}
}

func (q Query) Upstream(name string) Query {
	return q.keep(func(c Call) bool { return c.Provider == name })
}

func (q Query) Model(model string) Query {
	return q.keep(func(c Call) bool {
		return c.Normalized.Model.Requested == model || c.Normalized.Model.Returned == model
	})
}

func (q Query) APIType(apiType string) Query {
	return q.keep(func(c Call) bool {
		return c.APIFamily == apiType || strings.TrimPrefix(c.APIFamily, "openai.") == apiType
	})
}

func (q Query) Status(status int) Query {
	return q.keep(func(c Call) bool { return c.HTTPStatus == status })
}

func (q Query) Errors() Query {
	return q.keep(func(c Call) bool { return c.Normalized.Error != nil })
}

func (q Query) Retries() Query {
	return q.keep(func(c Call) bool { return c.RetryCount != nil && *c.RetryCount > 0 })
}

func (q Query) Index(i int) Query {
	if i < 0 || i >= len(q.calls) {
		return Query{trace: q.trace}
	}
	return Query{trace: q.trace, calls: []Call{q.calls[i]}}
}

func (q Query) keep(fn func(Call) bool) Query {
	out := make([]Call, 0, len(q.calls))
	for _, call := range q.calls {
		if fn(call) {
			out = append(out, call)
		}
	}
	return Query{trace: q.trace, calls: out}
}

type Assertions struct {
	t     testing.TB
	trace Trace
	fatal bool
}

func (a *Assertions) Trace() Trace {
	return a.trace
}

func (a *Assertions) fail(format string, args ...any) bool {
	a.t.Helper()
	msg := fmt.Sprintf(format, args...)
	msg += "\n\nTrace summary:\n" + a.trace.Summary()
	if a.fatal {
		a.t.Fatal(msg)
		return false
	}
	a.t.Error(msg)
	return false
}

func (a *Assertions) CallCount(want int, opts ...CallCountOption) bool {
	a.t.Helper()
	var options CallCountOptions
	for _, opt := range opts {
		opt(&options)
	}
	got := 0
	for _, call := range a.trace.Calls {
		if options.ExcludeRetries && call.RetryCount != nil && *call.RetryCount > 0 {
			continue
		}
		got++
	}
	if got != want {
		return a.fail("LLM call count mismatch: got %d, want %d", got, want)
	}
	return true
}

func (a *Assertions) Retries(want int) bool {
	a.t.Helper()
	got := 0
	for _, call := range a.trace.Calls {
		if call.RetryCount != nil && *call.RetryCount > 0 {
			got++
		}
	}
	if got != want {
		return a.fail("retry call count mismatch: got %d, want %d", got, want)
	}
	return true
}

func (a *Assertions) NoRetries() bool {
	a.t.Helper()
	return a.Retries(0)
}

func (a *Assertions) ToolCalled(name string, opts ...ToolAssertionOption) bool {
	a.t.Helper()
	matches := matchingTools(a.trace.Calls, name, opts...)
	if len(matches) == 0 {
		return a.fail("expected tool %q to be called", name)
	}
	return true
}

func (a *Assertions) ToolNotCalled(name string) bool {
	a.t.Helper()
	if len(matchingTools(a.trace.Calls, name)) > 0 {
		return a.fail("expected tool %q not to be called", name)
	}
	return true
}

func (a *Assertions) ToolCalledTimes(name string, n int, opts ...ToolAssertionOption) bool {
	a.t.Helper()
	got := len(matchingTools(a.trace.Calls, name, opts...))
	if got != n {
		return a.fail("tool %q call count mismatch: got %d, want %d", name, got, n)
	}
	return true
}

func (a *Assertions) ToolOrder(names ...string) bool {
	a.t.Helper()
	got := toolNames(a.trace.Calls)
	if !equalStrings(got, names) {
		return a.fail("tool order mismatch: got %v, want %v", got, names)
	}
	return true
}

func (a *Assertions) ToolBefore(before, after string) bool {
	a.t.Helper()
	positions := map[string]int{}
	for i, name := range toolNames(a.trace.Calls) {
		if _, ok := positions[name]; !ok {
			positions[name] = i
		}
	}
	b, bok := positions[before]
	af, aok := positions[after]
	if !bok || !aok || b >= af {
		return a.fail("expected tool %q before %q, got order %v", before, after, toolNames(a.trace.Calls))
	}
	return true
}

func (a *Assertions) ParallelToolCalls(names ...string) bool {
	a.t.Helper()
	want := append([]string(nil), names...)
	sort.Strings(want)
	for _, call := range a.trace.Calls {
		if len(call.Normalized.ToolCalls) < len(want) {
			continue
		}
		got := make([]string, 0, len(call.Normalized.ToolCalls))
		for _, tool := range call.Normalized.ToolCalls {
			got = append(got, tool.Name)
		}
		sort.Strings(got)
		if containsAllStrings(got, want) {
			return true
		}
	}
	return a.fail("expected parallel tool calls %v in a single LLM call", names)
}

func (a *Assertions) ToolResultFedBack(callID string) bool {
	a.t.Helper()
	for _, call := range a.trace.Calls {
		for _, result := range call.Normalized.ToolResultsIn {
			if result.CallID == callID {
				return true
			}
		}
	}
	return a.fail("expected tool result for call id %q to be fed back", callID)
}

func (a *Assertions) Model(model string) bool {
	a.t.Helper()
	for _, call := range a.trace.Calls {
		if call.Normalized.Model.Requested == model || call.Normalized.Model.Returned == model {
			return true
		}
	}
	return a.fail("expected model %q", model)
}

func (a *Assertions) RequestParam(name string, want any) bool {
	a.t.Helper()
	for _, call := range a.trace.Calls {
		var body map[string]any
		if json.Unmarshal(call.RawRequest, &body) == nil && jsonDeepEqual(body[name], want) {
			return true
		}
	}
	return a.fail("expected request param %s=%s", name, mustJSON(want))
}

func (a *Assertions) FinishReason(reason string) bool {
	a.t.Helper()
	for _, call := range a.trace.Calls {
		if call.Normalized.FinishReason == reason || call.Normalized.NativeFinishReason == reason {
			return true
		}
	}
	return a.fail("expected finish reason %q", reason)
}

func (a *Assertions) TokensBelow(maxTotal int) bool {
	a.t.Helper()
	return a.tokenBound(maxTotal, true)
}

func (a *Assertions) TokensAtMost(maxTotal int) bool {
	a.t.Helper()
	return a.tokenBound(maxTotal, false)
}

func (a *Assertions) tokenBound(maxTotal int, strict bool) bool {
	a.t.Helper()
	total, known := 0, false
	for _, call := range a.trace.Calls {
		if call.Normalized.Usage == nil {
			continue
		}
		if call.Normalized.Usage.TotalTokens != nil {
			total += *call.Normalized.Usage.TotalTokens
			known = true
		} else {
			if call.Normalized.Usage.InputTokens != nil {
				total += *call.Normalized.Usage.InputTokens
				known = true
			}
			if call.Normalized.Usage.OutputTokens != nil {
				total += *call.Normalized.Usage.OutputTokens
				known = true
			}
		}
	}
	if !known {
		return a.fail("no token usage reported")
	}
	if strict && total >= maxTotal {
		return a.fail("token total too high: got %d, want <%d", total, maxTotal)
	}
	if !strict && total > maxTotal {
		return a.fail("token total too high: got %d, want <=%d", total, maxTotal)
	}
	return true
}

func (a *Assertions) LatencyBelow(d time.Duration) bool {
	a.t.Helper()
	for _, call := range a.trace.Calls {
		if time.Duration(call.LatencyMS)*time.Millisecond >= d {
			return a.fail("call #%d latency %s exceeds %s", call.Seq, time.Duration(call.LatencyMS)*time.Millisecond, d)
		}
	}
	return true
}

func (a *Assertions) TTFTBelow(d time.Duration) bool {
	a.t.Helper()
	for _, call := range a.trace.Calls {
		if call.FirstByteAt == nil {
			continue
		}
		ttft := call.FirstByteAt.Sub(call.StartedAt)
		if ttft >= d {
			return a.fail("call #%d TTFT %s exceeds %s", call.Seq, ttft, d)
		}
	}
	return true
}

func (a *Assertions) NoErrors() bool {
	a.t.Helper()
	for _, call := range a.trace.Calls {
		if call.Normalized.Error != nil {
			return a.fail("unexpected error in call #%d: %+v", call.Seq, call.Normalized.Error)
		}
	}
	return true
}

func (a *Assertions) HasError() bool {
	a.t.Helper()
	for _, call := range a.trace.Calls {
		if call.Normalized.Error != nil {
			return true
		}
	}
	return a.fail("expected at least one LLM error")
}

func (a *Assertions) MidStreamError() bool {
	a.t.Helper()
	for _, call := range a.trace.Calls {
		if call.Normalized.Error != nil && call.Normalized.Error.MidStream {
			return true
		}
	}
	return a.fail("expected an error inside an HTTP-200 stream")
}

func (a *Assertions) SystemPromptContains(substr string) bool {
	a.t.Helper()
	for _, call := range a.trace.Calls {
		if strings.Contains(call.Normalized.SystemPrompt, substr) {
			return true
		}
	}
	return a.fail("expected system prompt to contain %q", substr)
}

func (a *Assertions) ResponseContains(substr string) bool {
	a.t.Helper()
	for _, call := range a.trace.Calls {
		if strings.Contains(call.Normalized.OutputText, substr) {
			return true
		}
	}
	return a.fail("expected response content to contain %q", substr)
}

func (a *Assertions) ResponseMatches(pattern string) bool {
	a.t.Helper()
	re, err := regexp.Compile(pattern)
	if err != nil {
		return a.fail("invalid response regexp %q: %v", pattern, err)
	}
	for _, call := range a.trace.Calls {
		if re.MatchString(call.Normalized.OutputText) {
			return true
		}
	}
	return a.fail("expected response content to match %q", pattern)
}

func (a *Assertions) ResponseJSONValid() bool {
	a.t.Helper()
	for _, call := range a.trace.Calls {
		if json.Valid([]byte(call.Normalized.OutputText)) {
			return true
		}
	}
	return a.fail("expected at least one response to contain valid JSON")
}

func (a *Assertions) ResponseJSONSchemaValid(schema map[string]any) bool {
	a.t.Helper()
	for _, call := range a.trace.Calls {
		var value any
		if json.Unmarshal([]byte(call.Normalized.OutputText), &value) == nil && validateBasicSchema(value, schema) == nil {
			return true
		}
	}
	return a.fail("expected at least one response to satisfy schema %s", mustJSON(schema))
}

type ToolAssertionOption func(*toolAssertion)

type toolAssertion struct {
	matchers []func(ToolCall) error
}

func ArgsSubset(want map[string]any) ToolAssertionOption {
	return func(a *toolAssertion) {
		a.matchers = append(a.matchers, func(tc ToolCall) error {
			var got any
			if len(tc.Arguments) == 0 {
				got = map[string]any{}
			} else if err := json.Unmarshal(tc.Arguments, &got); err != nil {
				return fmt.Errorf("args are not valid JSON: %v", err)
			}
			if !jsonSubset(got, want) {
				return fmt.Errorf("args %s do not include subset %s", string(tc.Arguments), mustJSON(want))
			}
			return nil
		})
	}
}

func ArgPath(path string, want any) ToolAssertionOption {
	return func(a *toolAssertion) {
		a.matchers = append(a.matchers, func(tc ToolCall) error {
			var got any
			if err := json.Unmarshal(tc.Arguments, &got); err != nil {
				return fmt.Errorf("args are not valid JSON: %v", err)
			}
			value, ok := jsonPath(got, path)
			if !ok || !jsonDeepEqual(value, want) {
				return fmt.Errorf("arg path %q = %s, want %s", path, mustJSON(value), mustJSON(want))
			}
			return nil
		})
	}
}

func ArgsMatch(fn func(json.RawMessage) error) ToolAssertionOption {
	return func(a *toolAssertion) {
		a.matchers = append(a.matchers, func(tc ToolCall) error { return fn(tc.Arguments) })
	}
}

func matchingTools(calls []Call, name string, opts ...ToolAssertionOption) []ToolCall {
	assertion := toolAssertion{}
	for _, opt := range opts {
		opt(&assertion)
	}
	var out []ToolCall
	for _, call := range calls {
		for _, tool := range call.Normalized.ToolCalls {
			if tool.Name != name {
				continue
			}
			ok := true
			for _, matcher := range assertion.matchers {
				if matcher(tool) != nil {
					ok = false
					break
				}
			}
			if ok {
				out = append(out, tool)
			}
		}
	}
	return out
}

func toolNames(calls []Call) []string {
	var out []string
	for _, call := range calls {
		for _, tool := range call.Normalized.ToolCalls {
			out = append(out, tool.Name)
		}
	}
	return out
}

func jsonSubset(got any, want any) bool {
	switch wantTyped := want.(type) {
	case map[string]any:
		gotMap, ok := got.(map[string]any)
		if !ok {
			return false
		}
		for key, wantValue := range wantTyped {
			if !jsonSubset(gotMap[key], wantValue) {
				return false
			}
		}
		return true
	case []any:
		gotSlice, ok := got.([]any)
		if !ok || len(gotSlice) < len(wantTyped) {
			return false
		}
		for i, wantValue := range wantTyped {
			if !jsonSubset(gotSlice[i], wantValue) {
				return false
			}
		}
		return true
	default:
		return jsonDeepEqual(got, want)
	}
}

func jsonPath(value any, path string) (any, bool) {
	current := value
	for _, part := range strings.Split(path, ".") {
		if part == "" || part == "$" {
			continue
		}
		switch typed := current.(type) {
		case map[string]any:
			var ok bool
			current, ok = typed[part]
			if !ok {
				return nil, false
			}
		case []any:
			var idx int
			if _, err := fmt.Sscanf(part, "%d", &idx); err != nil || idx < 0 || idx >= len(typed) {
				return nil, false
			}
			current = typed[idx]
		default:
			return nil, false
		}
	}
	return current, true
}

func jsonDeepEqual(got, want any) bool {
	gotBytes, _ := json.Marshal(got)
	wantBytes, _ := json.Marshal(want)
	var gotJSON any
	var wantJSON any
	_ = json.Unmarshal(gotBytes, &gotJSON)
	_ = json.Unmarshal(wantBytes, &wantJSON)
	return fmt.Sprintf("%#v", gotJSON) == fmt.Sprintf("%#v", wantJSON)
}

func validateBasicSchema(value any, schema map[string]any) error {
	if typ, _ := schema["type"].(string); typ != "" {
		if !matchesJSONType(value, typ) {
			return fmt.Errorf("type mismatch: got %T want %s", value, typ)
		}
	}
	if required := requiredKeys(schema["required"]); len(required) > 0 {
		obj, _ := value.(map[string]any)
		for _, key := range required {
			if _, ok := obj[key]; !ok {
				return fmt.Errorf("missing required property %q", key)
			}
		}
	}
	if props, ok := schema["properties"].(map[string]any); ok {
		obj, _ := value.(map[string]any)
		for key, rawSchema := range props {
			if child, ok := obj[key]; ok {
				childSchema, _ := rawSchema.(map[string]any)
				if childSchema != nil {
					if err := validateBasicSchema(child, childSchema); err != nil {
						return fmt.Errorf("%s: %w", key, err)
					}
				}
			}
		}
	}
	return nil
}

func requiredKeys(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if key, ok := item.(string); ok {
				out = append(out, key)
			}
		}
		return out
	default:
		return nil
	}
}

func matchesJSONType(value any, typ string) bool {
	switch typ {
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "number":
		_, ok := value.(float64)
		return ok
	case "integer":
		n, ok := value.(float64)
		return ok && n == float64(int64(n))
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "null":
		return value == nil
	default:
		return true
	}
}

func compactJSON(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "{}"
	}
	var b bytes.Buffer
	if json.Compact(&b, []byte(raw)) == nil {
		return b.String()
	}
	return raw
}

func equalStrings(a, b []string) bool {
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

func containsAllStrings(got, want []string) bool {
	counts := map[string]int{}
	for _, value := range got {
		counts[value]++
	}
	for _, value := range want {
		if counts[value] == 0 {
			return false
		}
		counts[value]--
	}
	return true
}

func getenvDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
