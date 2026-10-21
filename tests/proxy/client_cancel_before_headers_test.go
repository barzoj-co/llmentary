// SPDX-License-Identifier: AGPL-3.0-only

package proxy_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/barzoj-co/llmentary/internal/proxy"
)

func slowHeaders(hold time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		timer := time.NewTimer(hold)
		defer timer.Stop()
		select {
		case <-r.Context().Done():
			return
		case <-timer.C:
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"id":"r","model":"gpt-fixture","status":"completed","output":[]}`)
	}
}

func postCancelAfter(t *testing.T, proxyURL, path, session string, body []byte, after time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), after)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, proxyURL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(proxy.SessionHeaderName, session)
	res, err := http.DefaultClient.Do(req)
	if res != nil {
		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
	}
	if err == nil {
		t.Fatal("expected client cancel error")
	}
}

func assertBeforeHeadersAbort(t *testing.T, call map[string]any) {
	t.Helper()
	n := normalized(call)
	if n["client_aborted"] != true {
		t.Fatalf("client_aborted=%v", n["client_aborted"])
	}
	if call["http_status"] == float64(http.StatusBadGateway) || call["http_status"] == http.StatusBadGateway {
		t.Fatalf("http_status=502")
	}
	if call["upstream_called"] != true {
		t.Fatalf("upstream_called=%v", call["upstream_called"])
	}
	if call["stream"] != true {
		t.Fatalf("stream=%v", call["stream"])
	}
	if n["finish_reason"] != "cancelled" {
		t.Fatalf("finish_reason=%v", n["finish_reason"])
	}
	errObj, _ := n["error"].(map[string]any)
	if fmtString(errObj["normalized"]) != "client_aborted" {
		t.Fatalf("error=%v", n["error"])
	}
	for _, w := range asAnySlice(n["parser_warnings"]) {
		if strings.Contains(fmtString(w), "strict decode failed") {
			t.Fatalf("decode warning: %v", n["parser_warnings"])
		}
	}
}

func TestClientCancelBeforeHeaders(t *testing.T) {
	const loops = 20
	p := startSpecProxy(t, "off", "relaxed", slowHeaders(time.Second))
	body := []byte(`{"model":"gpt-fixture","input":"tell a long story","stream":true}`)
	for i := 0; i < loops; i++ {
		sid := newSession()
		postCancelAfter(t, p.proxyURL, "/v1/responses", sid, body, 100*time.Millisecond)
		assertBeforeHeadersAbort(t, lastCall(t, waitSession(t, p.controlURL, sid)))
	}
}

func TestClientCancelBeforeHeadersLogsProxyResponse(t *testing.T) {
	logBuf := &syncLogBuf{}
	p := startSpecProxy(t, "off", "relaxed", slowHeaders(time.Second), func(cfg *proxy.Config) {
		cfg.Logger = slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	})
	sid := newSession()
	postCancelAfter(t, p.proxyURL, "/v1/responses", sid, []byte(`{"model":"gpt-fixture","input":"hi","stream":true}`), 100*time.Millisecond)
	_ = waitSession(t, p.controlURL, sid)
	var n int
	for _, line := range strings.Split(logBuf.String(), "\n") {
		if strings.Contains(line, `msg="proxy response"`) && strings.Contains(line, "client_aborted=true") {
			n++
			if !strings.Contains(line, "status=-") || !strings.Contains(line, "phase=before_headers") {
				t.Fatalf("abort log: %s", line)
			}
		}
	}
	if n != 1 {
		t.Fatalf("proxy response abort lines=%d log=%s", n, logBuf.String())
	}
}

func TestUpstreamRefusedStillNetwork502(t *testing.T) {
	logBuf := &syncLogBuf{}
	p := startSpecProxy(t, "off", "relaxed", chatOK(), func(cfg *proxy.Config) {
		cfg.Upstreams = nil
		if err := cfg.AddUpstream("default=http://127.0.0.1:1"); err != nil {
			t.Fatal(err)
		}
		cfg.Logger = slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	})
	sid := newSession()
	status, _, _, session := postRaw(t, p, "/v1/responses", sid, nil, []byte(`{"model":"gpt-fixture","input":"hi","stream":true}`))
	if status != http.StatusBadGateway {
		t.Fatalf("status=%d", status)
	}
	call := lastCall(t, session)
	n := normalized(call)
	if n["client_aborted"] == true {
		t.Fatalf("refused marked client_aborted: %+v", n)
	}
	if call["http_status"] != float64(http.StatusBadGateway) {
		t.Fatalf("http_status=%v", call["http_status"])
	}
	errObj, _ := n["error"].(map[string]any)
	if fmtString(errObj["normalized"]) != "network" {
		t.Fatalf("error=%v", n["error"])
	}
	var lines int
	for _, line := range strings.Split(logBuf.String(), "\n") {
		if strings.Contains(line, `msg="proxy response"`) {
			lines++
		}
	}
	if lines != 1 {
		t.Fatalf("proxy response lines=%d log=%s", lines, logBuf.String())
	}
}
