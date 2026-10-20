// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"bytes"
	"log/slog"
	"net/url"
	"strings"
	"testing"
)

func TestValidateAppliesDefaultUpstream(t *testing.T) {
	t.Parallel()
	var logBuf bytes.Buffer
	cfg := Config{
		ListenAddr:  "127.0.0.1:7777",
		ControlAddr: "127.0.0.1:7778",
		Logger:      slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelInfo})),
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	got, ok := cfg.Upstreams[DefaultUpstreamName]
	if !ok || got == nil || got.String() != DefaultUpstreamURL {
		t.Fatalf("upstreams=%v want %s=%s", cfg.Upstreams, DefaultUpstreamName, DefaultUpstreamURL)
	}
	if !strings.Contains(logBuf.String(), DefaultUpstreamLog) {
		t.Fatalf("startup log %q does not contain %q", logBuf.String(), DefaultUpstreamLog)
	}
}

func TestValidateKeepsConfiguredUpstream(t *testing.T) {
	t.Parallel()
	cfg := Config{ListenAddr: "127.0.0.1:7777", ControlAddr: "127.0.0.1:7778"}
	if err := cfg.AddUpstream("openrouter=https://openrouter.ai/api/v1"); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Upstreams[DefaultUpstreamName]; ok {
		t.Fatalf("default openai upstream should not be added when one is configured: %v", cfg.Upstreams)
	}
	if cfg.Upstreams["openrouter"].Host != "openrouter.ai" {
		t.Fatalf("upstreams=%v", cfg.Upstreams)
	}
}

func TestUpstreamLoopGuardTable(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		listen    string
		upstream  string
		wantError bool
	}{
		{name: "localhost_same_port", listen: "127.0.0.1:7777", upstream: "http://localhost:7777/v1", wantError: true},
		{name: "127_same_port", listen: "127.0.0.1:7777", upstream: "http://127.0.0.1:7777/v1", wantError: true},
		{name: "0_0_0_0_same_port", listen: "127.0.0.1:7777", upstream: "http://0.0.0.0:7777/v1", wantError: true},
		{name: "ipv6_loopback_same_port", listen: "[::1]:7777", upstream: "http://[::1]:7777/v1", wantError: true},
		{name: "listen_unspecified_loop_upstream", listen: ":7777", upstream: "http://127.0.0.1:7777/v1", wantError: true},
		{name: "exact_listen_host", listen: "10.0.0.5:7777", upstream: "http://10.0.0.5:7777/v1", wantError: true},
		{name: "lan_listen_localhost_upstream_allowed", listen: "10.0.0.5:7777", upstream: "http://localhost:7777/v1", wantError: false},
		{name: "different_port_allowed", listen: "127.0.0.1:7777", upstream: "http://127.0.0.1:8888/v1", wantError: false},
		{name: "openai_allowed", listen: ":7777", upstream: DefaultUpstreamURL, wantError: false},
		{name: "ephemeral_listen_skips", listen: ":0", upstream: "http://127.0.0.1:0/v1", wantError: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := Config{ListenAddr: tc.listen, ControlAddr: "127.0.0.1:7778"}
			if err := cfg.AddUpstream("openai=" + tc.upstream); err != nil {
				t.Fatal(err)
			}
			err := cfg.Validate()
			if tc.wantError && err == nil {
				t.Fatal("expected loop-guard error")
			}
			if !tc.wantError && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantError && err != nil {
				if !strings.Contains(err.Error(), "proxy loop") {
					t.Fatalf("error %q should mention proxy loop", err)
				}
				if !strings.Contains(err.Error(), tc.listen) {
					t.Fatalf("error %q should mention listen address %s", err, tc.listen)
				}
			}
		})
	}
}

func TestIsSelfUpstreamPorts(t *testing.T) {
	t.Parallel()
	u, err := url.Parse("https://api.openai.com/v1")
	if err != nil {
		t.Fatal(err)
	}
	if isSelfUpstream(":443", u) {
		t.Fatal("openai on :443 must not be treated as a loop")
	}
}
