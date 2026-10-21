// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/barzoj-co/llmentary/internal/proxy"
)

func TestEnumFlagAndRunMoreFlags(t *testing.T) {
	clearProxyEnv(t)
	var e enumFlag
	if err := e.Set("warn"); err != nil || e.String() != "warn" {
		t.Fatalf("%v %s", err, e.String())
	}

	orig := listenAndServe
	t.Cleanup(func() { listenAndServe = orig })
	listenAndServe = func(context.Context, *proxy.Server) error { return nil }

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"--listen", "127.0.0.1:0",
		"--control", "127.0.0.1:0",
		"--spec-validation", "warn",
		"--spec-profile", "relaxed",
		"--spec-log-max", "0",
		"--session-from", "header,default",
		"--default-session", "cli-sess",
		"--allow-path-prefix", "custom",
		"--allow-path-prefix", "other",
		"--propagate-trace", "off",
		"--upstream", "openai=https://api.openai.com/v1",
		"--version",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("version after flags exit=%d stderr=%s", code, stderr.String())
	}
	if !strings.HasPrefix(stdout.String(), "llmentary ") {
		t.Fatalf("stdout=%q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{
		"--listen", "127.0.0.1:0",
		"--control", "127.0.0.1:0",
		"--allow-path-prefix", "custom",
		"--spec-validation-max-bytes", "1024",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("start exit=%d stderr=%s", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"--spec-validation", "nope", "--listen", "127.0.0.1:0", "--control", "127.0.0.1:0"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("bad spec mode exit=%d stderr=%s", code, stderr.String())
	}
}

func TestRunConfigFromEnvError(t *testing.T) {
	clearProxyEnv(t)
	t.Setenv("LLMENTARY_SPEC_LOG_MAX", "nope")
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr); code != 1 {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if stderr.Len() == 0 {
		t.Fatal("expected env error")
	}
}

func TestNonNegIntFlagString(t *testing.T) {
	f := nonNegIntFlag(4)
	if f.String() != "4" {
		t.Fatal(f.String())
	}
	u := upstreamFlags{"a=https://x", "b=https://y"}
	if u.String() == "" {
		t.Fatal("upstream flags")
	}
}
