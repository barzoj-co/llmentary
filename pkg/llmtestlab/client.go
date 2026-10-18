// SPDX-License-Identifier: MIT

package llmtestlab

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

type Client struct {
	baseURL      *url.URL
	httpClient   *http.Client
	controlToken string
}

func NewClient(proxyControlURL string, opts ...ClientOption) (*Client, error) {
	u, err := url.Parse(proxyControlURL)
	if err != nil {
		return nil, err
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("proxy control URL must include scheme and host")
	}
	c := &Client{baseURL: u, httpClient: http.DefaultClient, controlToken: os.Getenv("LLMTESTLAB_CONTROL_TOKEN")}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

func WithControlToken(token string) ClientOption {
	return func(c *Client) {
		c.controlToken = token
	}
}

type ClientOption func(*Client)

func WithHTTPClient(httpClient *http.Client) ClientOption {
	return func(c *Client) {
		if httpClient != nil {
			c.httpClient = httpClient
		}
	}
}

func (c *Client) SessionHeader(sessionID string) (string, string) {
	return Header(sessionID)
}

func (c *Client) NewSession() string {
	return NewSessionID()
}

func (c *Client) Calls(ctx context.Context, sessionID string) ([]Call, error) {
	var session Session
	if err := c.getJSON(ctx, "/sessions/"+url.PathEscape(sessionID), nil, &session); err != nil {
		return nil, err
	}
	return session.Calls, nil
}

func (c *Client) Wait(ctx context.Context, sessionID string, quiet, timeout time.Duration) ([]Call, error) {
	q := url.Values{}
	if quiet > 0 {
		q.Set("quiet", quiet.String())
	}
	if timeout > 0 {
		q.Set("timeout", timeout.String())
	}
	var session Session
	if err := c.getJSON(ctx, "/sessions/"+url.PathEscape(sessionID)+"/wait", q, &session); err != nil {
		return nil, err
	}
	return session.Calls, nil
}

func (c *Client) OTLP(ctx context.Context, sessionID string) (json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.getJSON(ctx, "/sessions/"+url.PathEscape(sessionID)+"/otlp", nil, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func (c *Client) Delete(ctx context.Context, sessionID string) error {
	u := c.resolve("/sessions/" + url.PathEscape(sessionID))
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u.String(), nil)
	if err != nil {
		return err
	}
	c.authorize(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("llmtestlab delete failed: status=%d body=%s", resp.StatusCode, string(body))
	}
	return nil
}

func (c *Client) getJSON(ctx context.Context, path string, q url.Values, out any) error {
	u := c.resolve(path)
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	c.authorize(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("llmtestlab get failed: status=%d body=%s", resp.StatusCode, string(body))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) resolve(path string) *url.URL {
	u := *c.baseURL
	u.Path = strings.TrimRight(c.baseURL.Path, "/") + path
	return &u
}

func (c *Client) authorize(req *http.Request) {
	if c.controlToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.controlToken)
	}
}

func AssertToolCalled(t testing.TB, calls []Call, name string, wantArgs any) {
	t.Helper()
	for _, call := range calls {
		for _, toolCall := range call.Normalized.ToolCalls {
			if toolCall.Name != name {
				continue
			}
			if wantArgs == nil || jsonMatches(t, toolCall.Arguments, wantArgs) {
				return
			}
		}
	}
	t.Fatalf("expected tool %q to be called with args %s; calls=%d", name, mustJSON(wantArgs), len(calls))
}

func ToolCalled(t testing.TB, calls []Call, name string, wantArgs any) {
	t.Helper()
	AssertToolCalled(t, calls, name, wantArgs)
}

func AssertToolNotCalled(t testing.TB, calls []Call, name string) {
	t.Helper()
	for _, call := range calls {
		for _, toolCall := range call.Normalized.ToolCalls {
			if toolCall.Name == name {
				t.Fatalf("expected tool %q not to be called; got args=%s in call=%s", name, string(toolCall.Arguments), call.ID)
			}
		}
	}
}

func ToolNotCalled(t testing.TB, calls []Call, name string) {
	t.Helper()
	AssertToolNotCalled(t, calls, name)
}

func AssertToolOrder(t testing.TB, calls []Call, names ...string) {
	t.Helper()
	got := make([]string, 0)
	for _, call := range calls {
		for _, toolCall := range call.Normalized.ToolCalls {
			got = append(got, toolCall.Name)
		}
	}
	if !reflect.DeepEqual(got, names) {
		t.Fatalf("tool order mismatch: got=%v want=%v", got, names)
	}
}

func ToolOrder(t testing.TB, calls []Call, names ...string) {
	t.Helper()
	AssertToolOrder(t, calls, names...)
}

type CallCountOptions struct {
	ExcludeRetries bool
}

type CallCountOption func(*CallCountOptions)

func ExcludeRetries() CallCountOption {
	return func(o *CallCountOptions) {
		o.ExcludeRetries = true
	}
}

func AssertCallCount(t testing.TB, calls []Call, want int, opts ...CallCountOption) {
	t.Helper()
	var options CallCountOptions
	for _, opt := range opts {
		opt(&options)
	}
	got := 0
	for _, call := range calls {
		if options.ExcludeRetries && call.RetryCount != nil && *call.RetryCount > 0 {
			continue
		}
		got++
	}
	if got != want {
		t.Fatalf("call count mismatch: got=%d want=%d", got, want)
	}
}

func CallCount(t testing.TB, calls []Call, want int, opts ...CallCountOption) {
	t.Helper()
	AssertCallCount(t, calls, want, opts...)
}

func AssertTokensBelow(t testing.TB, calls []Call, maxTotal int) {
	t.Helper()
	total := 0
	known := false
	for _, call := range calls {
		usage := call.Normalized.Usage
		if usage == nil {
			continue
		}
		if usage.TotalTokens != nil {
			total += *usage.TotalTokens
			known = true
			continue
		}
		if usage.InputTokens != nil {
			total += *usage.InputTokens
			known = true
		}
		if usage.OutputTokens != nil {
			total += *usage.OutputTokens
			known = true
		}
	}
	if !known {
		t.Fatalf("no token usage was reported")
	}
	if total >= maxTotal {
		t.Fatalf("token total too high: got=%d want<%d", total, maxTotal)
	}
}

func TokensBelow(t testing.TB, calls []Call, maxTotal int) {
	t.Helper()
	AssertTokensBelow(t, calls, maxTotal)
}

func jsonMatches(t testing.TB, raw json.RawMessage, want any) bool {
	t.Helper()
	if want == nil {
		return true
	}
	wantBytes, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal wanted args: %v", err)
	}
	var gotValue any
	if len(raw) == 0 {
		gotValue = map[string]any{}
	} else if err := json.Unmarshal(raw, &gotValue); err != nil {
		return false
	}
	var wantValue any
	if err := json.Unmarshal(wantBytes, &wantValue); err != nil {
		t.Fatalf("unmarshal wanted args: %v", err)
	}
	return reflect.DeepEqual(gotValue, wantValue)
}

func mustJSON(v any) string {
	if v == nil {
		return "<any>"
	}
	var b bytes.Buffer
	if err := json.NewEncoder(&b).Encode(v); err != nil {
		return fmt.Sprintf("%v", v)
	}
	return strings.TrimSpace(b.String())
}
