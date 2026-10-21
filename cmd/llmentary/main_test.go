// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/barzoj-co/llmentary/internal/proxy"
)

func clearProxyEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"LLMENTARY_LISTEN", "LLMENTARY_CONTROL_LISTEN", "LLMENTARY_CONTROL_TOKEN",
		"LLMENTARY_UPSTREAMS", "LLMENTARY_SPEC_VALIDATION", "LLMENTARY_SPEC_PROFILE",
		"LLMENTARY_SPEC_VALIDATION_MAX_BYTES", "LLMENTARY_SPEC_LOG_MAX",
		"LLMENTARY_SESSION_FROM", "LLMENTARY_DEFAULT_SESSION",
		"LLMENTARY_ALLOW_PATH_PREFIXES", "LLMENTARY_PROPAGATE_TRACE",
	} {
		t.Setenv(key, "")
	}
}

func TestRunVersionShortCircuit(t *testing.T) {
	clearProxyEnv(t)
	for _, arg := range []string{"--version", "-version"} {
		var stdout, stderr bytes.Buffer
		if code := run([]string{arg, "--listen", "not-a-valid-flag-value-ignored"}, &stdout, &stderr); code != 0 {
			t.Fatalf("%s exit=%d stderr=%s", arg, code, stderr.String())
		}
		if !strings.HasPrefix(stdout.String(), "llmentary ") {
			t.Fatalf("%s stdout=%q", arg, stdout.String())
		}
		if stderr.Len() != 0 {
			t.Fatalf("%s wrote stderr: %s", arg, stderr.String())
		}
	}
}

func TestRunBadSessionFrom(t *testing.T) {
	clearProxyEnv(t)
	var stdout, stderr bytes.Buffer
	code := run([]string{"--session-from=nope"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit=%d want 2 stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "session-from") && !strings.Contains(stderr.String(), "nope") {
		t.Fatalf("stderr=%s", stderr.String())
	}
}

func TestRunBadUpstream(t *testing.T) {
	clearProxyEnv(t)
	var stdout, stderr bytes.Buffer
	code := run([]string{"--upstream", ":::not-a-url"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit=%d want 1 stderr=%s", code, stderr.String())
	}
	if stderr.Len() == 0 {
		t.Fatal("expected upstream error")
	}
}

func TestRunNonLoopbackControlRequiresToken(t *testing.T) {
	clearProxyEnv(t)
	var stdout, stderr bytes.Buffer
	code := run([]string{"--control", "0.0.0.0:7778"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit=%d want 2 stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "control-token") && !strings.Contains(stderr.String(), "loopback") {
		t.Fatalf("stderr=%s", stderr.String())
	}
}

func TestRunUnknownFlag(t *testing.T) {
	clearProxyEnv(t)
	var stdout, stderr bytes.Buffer
	code := run([]string{"--not-a-flag"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit=%d want 2 stderr=%s", code, stderr.String())
	}
}

func TestRunBadMaxBytesFlag(t *testing.T) {
	clearProxyEnv(t)
	var stdout, stderr bytes.Buffer
	code := run([]string{"--spec-validation-max-bytes=-1"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit=%d want 2 stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "non-negative") {
		t.Fatalf("stderr=%s", stderr.String())
	}
}

func TestRunStartsWithDefaults(t *testing.T) {
	clearProxyEnv(t)
	orig := listenAndServe
	t.Cleanup(func() { listenAndServe = orig })
	var gotAddr string
	listenAndServe = func(_ context.Context, server *proxy.Server) error {
		gotAddr = "called"
		_ = server
		return nil
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"--listen", "127.0.0.1:0", "--control", "127.0.0.1:0"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if gotAddr != "called" {
		t.Fatal("listenAndServe was not called")
	}
	if !strings.Contains(stderr.String(), "proxy listening") {
		t.Fatalf("startup log=%s", stderr.String())
	}
}

func TestRunListenError(t *testing.T) {
	clearProxyEnv(t)
	orig := listenAndServe
	t.Cleanup(func() { listenAndServe = orig })
	listenAndServe = func(context.Context, *proxy.Server) error {
		return context.Canceled
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"--listen", "127.0.0.1:0", "--control", "127.0.0.1:0"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit=%d want 1", code)
	}
	if !strings.Contains(stderr.String(), "canceled") {
		t.Fatalf("stderr=%s", stderr.String())
	}
}

func TestNonNegIntFlag(t *testing.T) {
	var f nonNegIntFlag
	if err := f.Set("32"); err != nil || int(f) != 32 {
		t.Fatalf("set 32: %v %d", err, f)
	}
	if err := f.Set("x"); err == nil {
		t.Fatal("expected invalid")
	}
	if err := f.Set("-3"); err == nil {
		t.Fatal("expected negative rejected")
	}
}
