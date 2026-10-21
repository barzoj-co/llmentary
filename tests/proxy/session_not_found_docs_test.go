// SPDX-License-Identifier: AGPL-3.0-only

package proxy_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestSessionNotFoundDocsUseListHint(t *testing.T) {
	root := filepath.Join("..", "..")
	old := "known sessions: GET /sessions"
	want := "list sessions with GET /sessions"
	files := []string{
		filepath.Join(root, "docs", "proxy.md"),
		filepath.Join(root, "docs", "session-lifecycle.md"),
		filepath.Join(root, "api", "control", "openapi.yaml"),
		filepath.Join(root, "internal", "proxy", "control.go"),
	}
	foundWant := false
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		if filepath.Base(path) == "openapi.yaml" {
			text = yamlStringBlob(t, raw)
		}
		if strings.Contains(text, old) {
			t.Errorf("%s still has %q", path, old)
		}
		if strings.Contains(text, want) {
			foundWant = true
		}
	}
	if !foundWant {
		t.Fatalf("none of the checked files contain %q", want)
	}
}

func yamlStringBlob(t *testing.T, raw []byte) string {
	t.Helper()
	var doc any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	var parts []string
	var walk func(any)
	walk = func(v any) {
		switch n := v.(type) {
		case string:
			parts = append(parts, n)
		case map[string]any:
			for _, child := range n {
				walk(child)
			}
		case []any:
			for _, child := range n {
				walk(child)
			}
		}
	}
	walk(doc)
	return strings.Join(parts, "\n")
}
