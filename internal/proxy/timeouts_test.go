// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestParseOptionalTimeout(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		raw     string
		want    time.Duration
		wantErr string
	}{
		{raw: "", want: 0},
		{raw: "  ", want: 0},
		{raw: "0", want: 0},
		{raw: "0s", want: 0},
		{raw: "0ms", want: 0},
		{raw: "30", want: 30 * time.Second},
		{raw: "30s", want: 30 * time.Second},
		{raw: "250ms", want: 250 * time.Millisecond},
		{raw: "10m", want: 10 * time.Minute},
		{raw: "nope", wantErr: "upstream-header-timeout"},
		{raw: "-1", wantErr: "upstream-header-timeout"},
		{raw: "-1s", wantErr: "upstream-header-timeout"},
		{raw: "11m", wantErr: "10m"},
	} {
		got, err := ParseOptionalTimeout("upstream-header-timeout", "LLMENTARY_UPSTREAM_HEADER_TIMEOUT", tc.raw)
		if tc.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("raw=%q err=%v want %q", tc.raw, err, tc.wantErr)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Fatalf("raw=%q got %s err=%v want %s", tc.raw, got, err, tc.want)
		}
	}
}

func TestValidateUpstreamTimeout(t *testing.T) {
	t.Parallel()
	if err := validateUpstreamTimeout("upstream-header-timeout", "ENV", 0); err != nil {
		t.Fatal(err)
	}
	if err := validateUpstreamTimeout("upstream-header-timeout", "ENV", -time.Second); err == nil {
		t.Fatal("negative")
	}
	if err := validateUpstreamTimeout("upstream-idle-timeout", "ENV", 11*time.Minute); err == nil {
		t.Fatal("cap")
	}
}

func TestHeaderIdleTimeoutErrors(t *testing.T) {
	t.Parallel()
	if !errors.Is(headerTimeoutError(80*time.Millisecond), ErrUpstreamHeaderTimeout) {
		t.Fatal("header wrap")
	}
	if !errors.Is(idleTimeoutError(time.Second), ErrUpstreamIdleTimeout) {
		t.Fatal("idle wrap")
	}
	if !isContextTimeout(context.Canceled) || !isContextTimeout(context.DeadlineExceeded) {
		t.Fatal("context")
	}
	if isContextTimeout(errors.New("other")) {
		t.Fatal("other")
	}
}

func TestHeaderTimeoutUpstreamCalledFollowsWroteRequest(t *testing.T) {
	store := NewMemoryStore()
	s := &Server{store: store, parser: TolerantParser{}}
	started := time.Now().UTC()
	_, id := store.Begin("s", Call{StartedAt: started, Upstream: Upstream{URL: "http://127.0.0.1/v1"}})
	ex := &proxyExchange{s: s, session: sessionInfo{ID: "s"}, callID: id, startedAt: started, rawReq: []byte(`{"model":"m"}`)}
	call := s.finishWithUpstreamHeaderTimeout(ex, headerTimeoutError(time.Millisecond))
	if call.UpstreamCalled {
		t.Fatal("wroteRequest false must leave upstream_called false")
	}
	ex.wroteRequest.Store(true)
	_, id = store.Begin("s", Call{StartedAt: started, Upstream: Upstream{URL: "http://127.0.0.1/v1"}})
	ex.callID = id
	call = s.finishWithUpstreamHeaderTimeout(ex, headerTimeoutError(time.Millisecond))
	if !call.UpstreamCalled {
		t.Fatal("wroteRequest true must set upstream_called")
	}
}

func TestValidateRejectsBadUpstreamTimeout(t *testing.T) {
	t.Parallel()
	cfg := Config{ListenAddr: "127.0.0.1:1", ControlAddr: "127.0.0.1:2", UpstreamHeaderTimeout: -time.Second}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "upstream-header-timeout") {
		t.Fatalf("header: %v", err)
	}
	cfg = Config{ListenAddr: "127.0.0.1:1", ControlAddr: "127.0.0.1:2", UpstreamIdleTimeout: 11 * time.Minute}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "upstream-idle-timeout") {
		t.Fatalf("idle: %v", err)
	}
}
