// SPDX-License-Identifier: AGPL-3.0-only

// Package tracetest is a test-only control-API client for the llmentary proxy.
// It starts a session, injects X-Test-Session, and fetches or waits for traces.
package tracetest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/barzoj-co/llmentary/internal/proxy"
)

const SessionHeaderName = proxy.SessionHeaderName

type Client struct {
	baseURL      *url.URL
	httpClient   *http.Client
	controlToken string
}

type ClientOption func(*Client)

func NewClient(proxyControlURL string, opts ...ClientOption) (*Client, error) {
	u, err := url.Parse(proxyControlURL)
	if err != nil {
		return nil, err
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("proxy control URL must include scheme and host")
	}
	c := &Client{baseURL: u, httpClient: http.DefaultClient, controlToken: os.Getenv("LLMENTARY_CONTROL_TOKEN")}
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

func WithHTTPClient(httpClient *http.Client) ClientOption {
	return func(c *Client) {
		if httpClient != nil {
			c.httpClient = httpClient
		}
	}
}

func (c *Client) NewSession() string {
	return NewSessionID()
}

func (c *Client) ControlURL() string {
	return strings.TrimRight(c.baseURL.String(), "/")
}

func (c *Client) Session(ctx context.Context, sessionID string) (proxy.Session, error) {
	var session proxy.Session
	if err := c.getJSON(ctx, "/sessions/"+url.PathEscape(sessionID), nil, &session); err != nil {
		return proxy.Session{}, err
	}
	return session, nil
}

func (c *Client) Calls(ctx context.Context, sessionID string) ([]proxy.Call, error) {
	session, err := c.Session(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return session.Calls, nil
}

func (c *Client) Wait(ctx context.Context, sessionID string, quiet, timeout time.Duration) ([]proxy.Call, error) {
	q := url.Values{}
	if quiet > 0 {
		q.Set("quiet", quiet.String())
	}
	if timeout > 0 {
		q.Set("timeout", timeout.String())
	}
	var session proxy.Session
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
		return fmt.Errorf("tracetest delete failed: status=%d body=%s", resp.StatusCode, string(body))
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
		return fmt.Errorf("tracetest get failed: status=%d body=%s", resp.StatusCode, string(body))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) resolve(path string) *url.URL {
	ref, err := url.Parse(path)
	if err != nil {
		u := *c.baseURL
		joined := strings.TrimRight(c.baseURL.Path, "/") + path
		u.Path = joined
		u.RawPath = joined
		return &u
	}
	return c.baseURL.ResolveReference(ref)
}

func (c *Client) authorize(req *http.Request) {
	if c.controlToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.controlToken)
	}
}

func NewSessionID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return hex.EncodeToString([]byte(time.Now().UTC().Format("20060102150405.000000000")))
	}
	return hex.EncodeToString(b[:])
}

func Header(sessionID string) (string, string) {
	return SessionHeaderName, sessionID
}
