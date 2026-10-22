// SPDX-License-Identifier: AGPL-3.0-only

package proxy_test

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestProxyResponseLogTokensOnErrors(t *testing.T) {
	t.Run("header_timeout", func(t *testing.T) {
		logBuf := &syncLogBuf{}
		p := startSpecProxy(t, "warn", "relaxed", stallHeadersForever(),
			withHeaderTimeout(80*time.Millisecond), withTestLogger(logBuf))
		sid := newSession()
		_, _, _, _ = postRaw(t, p, "/v1/chat/completions", sid, nil, chatJSONBody)
		var found bool
		for _, line := range strings.Split(logBuf.String(), "\n") {
			if strings.Contains(line, `msg="proxy response"`) && strings.Contains(line, "error=upstream_timeout") {
				if strings.Contains(line, "stream_truncated=") {
					t.Fatalf("header timeout logged truncation: %s", line)
				}
				found = true
			}
		}
		if !found {
			t.Fatalf("missing error=upstream_timeout\n%s", logBuf.String())
		}
	})
	t.Run("upstream_400", func(t *testing.T) {
		logBuf := &syncLogBuf{}
		p := startSpecProxy(t, "off", "relaxed", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"type":"invalid_request_error","code":"bad","message":"nope"}}`))
		}, withTestLogger(logBuf))
		sid := newSession()
		_, _, _, sess := postRaw(t, p, "/v1/chat/completions", sid, nil, chatJSONBody)
		errObj, _ := normalized(lastCall(t, sess))["error"].(map[string]any)
		if fmtString(errObj["normalized"]) != "bad_request" || fmtString(errObj["source"]) != "upstream" {
			t.Fatalf("error=%v", errObj)
		}
		var found bool
		for _, line := range strings.Split(logBuf.String(), "\n") {
			if strings.Contains(line, `msg="proxy response"`) && strings.Contains(line, "error=bad_request") {
				if strings.Contains(line, "nope") || strings.Contains(line, "Authorization") {
					t.Fatalf("log leaked body: %s", line)
				}
				found = true
			}
		}
		if !found {
			t.Fatalf("missing error=bad_request\n%s", logBuf.String())
		}
	})
	t.Run("upstream_500", func(t *testing.T) {
		p := startSpecProxy(t, "off", "relaxed", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":{"type":"server_error","message":"boom"}}`))
		})
		sid := newSession()
		_, _, _, sess := postRaw(t, p, "/v1/chat/completions", sid, nil, chatJSONBody)
		errObj, _ := normalized(lastCall(t, sess))["error"].(map[string]any)
		if fmtString(errObj["normalized"]) != "server" || fmtString(errObj["source"]) != "upstream" {
			t.Fatalf("error=%v", errObj)
		}
	})
	t.Run("mid_stream", func(t *testing.T) {
		p := startSpecProxy(t, "off", "relaxed", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("data: {\"id\":\"x\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"},\"finish_reason\":null}]}\n\n"))
			_, _ = w.Write([]byte("data: {\"error\":{\"type\":\"server_error\",\"message\":\"cut\"}}\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		})
		sid := newSession()
		_, _, _, sess := postRaw(t, p, "/v1/chat/completions", sid, nil, chatStreamBody)
		errObj, _ := normalized(lastCall(t, sess))["error"].(map[string]any)
		if fmtString(errObj["source"]) != "upstream" {
			t.Fatalf("mid-stream source=%v", errObj)
		}
		if errObj["mid_stream"] != true {
			t.Fatalf("mid_stream=%v", errObj)
		}
	})
	t.Run("clean", func(t *testing.T) {
		logBuf := &syncLogBuf{}
		p := startSpecProxy(t, "off", "relaxed", chatOK(), withTestLogger(logBuf))
		sid := newSession()
		_, _, _, _ = postRaw(t, p, "/v1/chat/completions", sid, nil, chatJSONBody)
		for _, line := range strings.Split(logBuf.String(), "\n") {
			if strings.Contains(line, `msg="proxy response"`) {
				if strings.Contains(line, "error=") || strings.Contains(line, "stream_truncated=") {
					t.Fatalf("clean call extra tokens: %s", line)
				}
			}
		}
	})
}
