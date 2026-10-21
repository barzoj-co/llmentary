// SPDX-License-Identifier: AGPL-3.0-only

package proxy_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
