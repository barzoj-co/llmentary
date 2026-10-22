// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestStampOpenAPIVersion(t *testing.T) {
	src := []byte("openapi: 3.1.0\ninfo:\n  title: llmentary control API\n  version: 0.1.0 # x-release-please-version\npaths: {}\n")
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
	if strings.Contains(string(got), "x-release-please-version") {
		t.Fatal("serve-time stamp should drop the release-please marker")
	}
}

func TestStampOpenAPIVersionDropsOnlyReleasePleaseCommentWhenVersionMatches(t *testing.T) {
	src := []byte("openapi: 3.1.0\ninfo:\n  title: x\n  version: 0.4.1 # x-release-please-version\npaths: {}\n")
	got := StampOpenAPIVersion(src, "0.4.1")
	want := []byte("openapi: 3.1.0\ninfo:\n  title: x\n  version: 0.4.1\npaths: {}\n")
	if string(got) != string(want) {
		t.Fatalf("got=%q want=%q", got, want)
	}
	if len(src)-len(got) != len(" # x-release-please-version") {
		t.Fatalf("delta=%d", len(src)-len(got))
	}
}

func TestOpenAPIInfoVersionIgnoresLineComment(t *testing.T) {
	src := []byte("openapi: 3.1.0\ninfo:\n  title: x\n  version: 0.4.0 # x-release-please-version\n")
	ver, err := OpenAPIInfoVersion(src)
	if err != nil {
		t.Fatal(err)
	}
	if ver != "0.4.0" {
		t.Fatalf("version=%q", ver)
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

func TestReleasePleaseOpenAPIUpdaterIsGeneric(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "..", "..")
	raw, err := os.ReadFile(filepath.Join(root, "release-please-config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Packages map[string]struct {
			ExtraFiles []map[string]any `json:"extra-files"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	pkg, ok := cfg.Packages["."]
	if !ok {
		t.Fatal("missing packages[\".\"]")
	}
	found := false
	for _, extra := range pkg.ExtraFiles {
		path, _ := extra["path"].(string)
		if path != "api/control/openapi.yaml" {
			continue
		}
		found = true
		typ, _ := extra["type"].(string)
		if typ != "generic" {
			t.Fatalf("openapi extra-file type=%q want generic (yaml jsonpath re-serializes the file)", typ)
		}
		if _, has := extra["jsonpath"]; has {
			t.Fatal("openapi extra-file still uses jsonpath")
		}
	}
	if !found {
		t.Fatal("api/control/openapi.yaml is not in extra-files")
	}

	spec, err := os.ReadFile(filepath.Join(root, "api", "control", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`(?m)^  version: \S+ # x-release-please-version\s*$`).Match(spec) {
		t.Fatal("api/control/openapi.yaml info.version is missing # x-release-please-version")
	}
}

func TestReleasePleaseBumpsPatchForFeatOnZeroX(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "..", "..")
	raw, err := os.ReadFile(filepath.Join(root, "release-please-config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		BumpMinorPreMajor         *bool `json:"bump-minor-pre-major"`
		BumpPatchForMinorPreMajor *bool `json:"bump-patch-for-minor-pre-major"`
		Packages                  map[string]struct {
			BumpMinorPreMajor         *bool `json:"bump-minor-pre-major"`
			BumpPatchForMinorPreMajor *bool `json:"bump-patch-for-minor-pre-major"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Packages) != 1 {
		t.Fatalf("packages=%d want 1", len(cfg.Packages))
	}
	pkg, ok := cfg.Packages["."]
	if !ok {
		t.Fatal("missing packages[\".\"]")
	}
	if pkg.BumpPatchForMinorPreMajor == nil || !*pkg.BumpPatchForMinorPreMajor {
		t.Fatal("packages[\".\"].bump-patch-for-minor-pre-major must be true so feat: on 0.x is a patch")
	}
	if pkg.BumpMinorPreMajor == nil || *pkg.BumpMinorPreMajor {
		t.Fatal("packages[\".\"].bump-minor-pre-major must be false; no 0.x minor bumps")
	}
	if cfg.BumpPatchForMinorPreMajor != nil && !*cfg.BumpPatchForMinorPreMajor {
		t.Fatal("root bump-patch-for-minor-pre-major must not override the package to false")
	}
	if cfg.BumpMinorPreMajor != nil && *cfg.BumpMinorPreMajor {
		t.Fatal("root bump-minor-pre-major must not override the package to true")
	}
}
