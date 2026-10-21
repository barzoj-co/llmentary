// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestStampOpenAPIVersion(t *testing.T) {
	src := []byte("openapi: 3.1.0\ninfo:\n  title: llmentary control API\n  version: 0.1.0\npaths: {}\n")
	got := StampOpenAPIVersion(src, "9.9.9")
	ver, err := OpenAPIInfoVersion(got)
	if err != nil {
		t.Fatal(err)
	}
	if ver != "9.9.9" {
		t.Fatalf("stamped=%q\n%s", ver, got)
	}
	if !strings.Contains(string(got), "openapi: 3.1.0") {
		t.Fatal("rewrote openapi document version")
	}
}

func TestCheckOpenAPIVersionFailsOnMismatch(t *testing.T) {
	src := []byte("openapi: 3.1.0\ninfo:\n  title: x\n  version: 0.0.0-wrong\n")
	err := CheckOpenAPIVersion(src, "9.9.9")
	if err == nil {
		t.Fatal("expected mismatch error")
	}
	if !strings.Contains(err.Error(), "0.0.0-wrong") || !strings.Contains(err.Error(), "9.9.9") {
		t.Fatalf("error=%v", err)
	}
}

func TestPackagedOpenAPIVersionMatchesManifest(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "..", "..")
	spec, err := os.ReadFile(filepath.Join(root, "api", "control", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	man, err := os.ReadFile(filepath.Join(root, ".release-please-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	want := ""
	for _, line := range strings.Split(string(man), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, `".":`) {
			want = strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, `".":`)), `",`)
		}
	}
	if want == "" {
		t.Fatal("manifest version missing")
	}
	if err := CheckOpenAPIVersion(spec, want); err != nil {
		t.Fatal(err)
	}
}
