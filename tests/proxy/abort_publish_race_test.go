// SPDX-License-Identifier: AGPL-3.0-only

package proxy_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/barzoj-co/llmentary/internal/proxy"
)

func TestAbortRecordPublishedAtomically(t *testing.T) {
	cases := []struct {
		name     string
		upstream http.HandlerFunc
		start    func(t *testing.T, p proxyProc, sid string) error
		assert   func(t *testing.T, call map[string]any)
	}{
		{
			name:     "before_headers",
			upstream: slowHeaders(2 * time.Second),
			start: func(t *testing.T, p proxyProc, sid string) error {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
				defer cancel()
				body := []byte(`{"model":"gpt-fixture","input":"tell a long story","stream":true}`)
				req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.proxyURL+"/v1/responses", bytes.NewReader(body))
				if err != nil {
					return err
				}
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set(proxy.SessionHeaderName, sid)
				res, err := http.DefaultClient.Do(req)
				if res != nil {
					_, _ = io.Copy(io.Discard, res.Body)
					_ = res.Body.Close()
				}
				if err == nil {
					return errWantCancel
				}
				return nil
			},
			assert: assertBeforeHeadersAbort,
		},
		{
			name:     "after_headers",
			upstream: chatStreamAbortAfterFirst(),
			start: func(_ *testing.T, p proxyProc, sid string) error {
				body := []byte(`{"model":"gpt-fixture","messages":[{"role":"user","content":"hi"}],"stream":true}`)
				return closeSSEAfterMarker(p.proxyURL, "/v1/chat/completions", sid, "chunk-one", body)
			},
			assert: func(t *testing.T, call map[string]any) {
				assertAfterHeadersAbort(t, call, "chunk-one")
			},
		},
		{
			name:     "after_headers_responses",
			upstream: responsesStreamAbortAfterFirst(),
			start: func(_ *testing.T, p proxyProc, sid string) error {
				body := []byte(`{"model":"gpt-fixture","input":"tell a long story","stream":true}`)
				return closeSSEAfterMarker(p.proxyURL, "/v1/responses", sid, "chunk-one", body)
			},
			assert: func(t *testing.T, call map[string]any) {
				assertAfterHeadersAbort(t, call, "chunk-one")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := proxy.NewMemoryStore()
			entered := make(chan struct{})
			release := make(chan struct{})
			var once sync.Once
			store.SetBeforeFinish(func() {
				once.Do(func() { close(entered) })
				<-release
			})
			p := startSpecProxyStore(t, store, "off", "relaxed", tc.upstream)
			// LIFO: close release before httptest.Server.Close so a Fatal
			// while Finish is parked on the hook cannot hang server shutdown.
			t.Cleanup(func() {
				select {
				case <-release:
				default:
					close(release)
				}
			})
			sid := newSession()

			reqDone := make(chan error, 1)
			go func() { reqDone <- tc.start(t, p, sid) }()

			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("Finish hook did not run")
			}

			waitStarted := make(chan struct{})
			waitDone := make(chan waitProbe, 1)
			go func() {
				close(waitStarted)
				status, body, err := getJSONErr(p.controlURL + "/sessions/" + sid + "/wait?min_calls=1&quiet=20ms&timeout=2s")
				waitDone <- waitProbe{status: status, body: body, err: err}
			}()
			<-waitStarted

			deadline := time.Now().Add(200 * time.Millisecond)
			for time.Now().Before(deadline) {
				status, _, body := getJSON(t, p.controlURL+"/sessions/"+sid)
				if status != http.StatusOK {
					t.Fatalf("GET during finalize status=%d body=%v", status, body)
				}
				if calls, _ := body["calls"].([]any); len(calls) != 0 {
					t.Fatalf("GET during finalize exposed in-flight call: %v", body)
				}
				select {
				case got := <-waitDone:
					t.Fatalf("wait returned during finalize status=%d body=%v", got.status, got.body)
				default:
				}
				time.Sleep(5 * time.Millisecond)
			}

			close(release)
			if err := <-reqDone; err != nil {
				t.Fatal(err)
			}
			got := <-waitDone
			if got.err != nil {
				t.Fatal(got.err)
			}
			if got.status != http.StatusOK {
				t.Fatalf("wait after publish status=%d body=%v", got.status, got.body)
			}
			call := lastCall(t, got.body)
			tc.assert(t, call)

			status, _, body := getJSON(t, p.controlURL+"/sessions/"+sid)
			if status != http.StatusOK {
				t.Fatalf("GET after publish status=%d", status)
			}
			tc.assert(t, lastCall(t, body))
		})
	}
}

type waitProbe struct {
	status int
	body   map[string]any
	err    error
}

func getJSONErr(url string) (int, map[string]any, error) {
	res, err := http.Get(url)
	if err != nil {
		return 0, nil, err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return res.StatusCode, nil, err
	}
	var body map[string]any
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &body); err != nil {
			return res.StatusCode, nil, err
		}
	}
	return res.StatusCode, body, nil
}

func closeSSEAfterMarker(proxyURL, path, session, marker string, body []byte) error {
	u, err := url.Parse(proxyURL + path)
	if err != nil {
		return err
	}
	conn, err := net.Dial("tcp", u.Host)
	if err != nil {
		return err
	}
	req := fmt.Sprintf("POST %s HTTP/1.1\r\nHost: %s\r\nContent-Type: application/json\r\nContent-Length: %d\r\n%s: %s\r\nConnection: close\r\n\r\n%s",
		u.RequestURI(), u.Host, len(body), proxy.SessionHeaderName, session, body)
	if _, err := conn.Write([]byte(req)); err != nil {
		_ = conn.Close()
		return err
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 4096)
	var got []byte
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			got = append(got, buf[:n]...)
			if sseFrameComplete(got, marker) {
				_ = conn.Close()
				return nil
			}
		}
		if err != nil {
			_ = conn.Close()
			if !sseFrameComplete(got, marker) {
				return fmt.Errorf("closed without complete %q frame: %v body=%q", marker, err, got)
			}
			return nil
		}
	}
}

var errWantCancel = errString("expected client cancel error")

type errString string

func (e errString) Error() string { return string(e) }
