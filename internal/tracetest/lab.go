// SPDX-License-Identifier: AGPL-3.0-only

package tracetest

import (
	"context"
	"net/http"
	"os"
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
		controlURL: getenvDefault("LLMENTARY_CONTROL_URL", defaultControlURL),
		proxyURL:   getenvDefault("LLMENTARY_PROXY_URL", defaultProxyURL),
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
		t.Fatalf("tracetest: create client: %v", err)
	}
	session := &Lab{
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
		_ = session.client.Delete(ctx, session.sessionID)
	})
	return session
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

func WithWait(quiet, timeout time.Duration) LabOption {
	return func(c *labConfig) {
		c.quiet = quiet
		c.timeout = timeout
	}
}

func (l *Lab) SessionID() string { return l.sessionID }

func (l *Lab) Header() (string, string) { return Header(l.sessionID) }

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

func (l *Lab) BaseURL(upstream string) string {
	if upstream == "" {
		return l.proxyURL
	}
	return l.proxyURL + "/u/" + strings.Trim(upstream, "/")
}

func (l *Lab) OpenAIBaseURL(upstream string) string {
	return l.BaseURL(upstream)
}

func (l *Lab) Wait(ctx context.Context) Trace {
	l.t.Helper()
	calls, err := l.client.Wait(ctx, l.sessionID, l.quiet, l.timeout)
	if err != nil {
		l.t.Fatalf("tracetest: wait for session %s: %v", l.sessionID, err)
	}
	return Trace{SessionID: l.sessionID, Calls: calls}
}

func (l *Lab) Calls(ctx context.Context) Trace {
	l.t.Helper()
	calls, err := l.client.Calls(ctx, l.sessionID)
	if err != nil {
		l.t.Fatalf("tracetest: fetch session %s: %v", l.sessionID, err)
	}
	return Trace{SessionID: l.sessionID, Calls: calls}
}

func getenvDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
