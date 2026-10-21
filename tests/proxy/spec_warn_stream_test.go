// SPDX-License-Identifier: AGPL-3.0-only

package proxy_test

import (
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/barzoj-co/llmentary/internal/proxy"
)

func fiveChunksMissingCreated() http.HandlerFunc {
	chunk := `data: {"id":"c","choices":[{"delta":{"content":"x"},"finish_reason":null}]}` + "\n\n"
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for i := 0; i < 5; i++ {
			_, _ = io.WriteString(w, chunk)
			if flusher != nil {
				flusher.Flush()
			}
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}
}

func TestStreamedResponseFindingsHaveEventAndCount(t *testing.T) {
	logBuf := &syncLogBuf{}
	p := startSpecProxy(t, "warn", "relaxed", fiveChunksMissingCreated(), func(cfg *proxy.Config) {
		cfg.Logger = slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))
		cfg.SpecLogMax = proxy.IntPtr(20)
	})
	sid := newSession()
	_, _, _, session := postRaw(t, p, "/v1/chat/completions", sid, nil, []byte(`{"model":"gpt-fixture","messages":[{"role":"user","content":"hi"}],"stream":true}`))
	n := normalized(lastCall(t, session))
	var foundCount5 bool
	for _, raw := range asAnySlice(n["spec_violations"]) {
		m, _ := raw.(map[string]any)
		if m["side"] != "response" {
			continue
		}
		msg, _ := m["message"].(string)
		if !strings.Contains(msg, "created") {
			continue
		}
		count, _ := m["count"].(float64)
		if count == 5 {
			foundCount5 = true
		}
	}
	if !foundCount5 {
		t.Fatalf("expected one stored finding with count=5 for missing created: %v", n["spec_violations"])
	}
	logs := logBuf.String()
	for i := 0; i < 5; i++ {
		if !strings.Contains(logs, "event="+strconv.Itoa(i)) {
			t.Fatalf("missing event=%d in WARN:\n%s", i, logs)
		}
	}
}
