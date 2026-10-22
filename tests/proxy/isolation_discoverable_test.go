// SPDX-License-Identifier: AGPL-3.0-only

package proxy_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsolationSpecIsDiscoverable(t *testing.T) {
	root := filepath.Join("..", "..")
	readme, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	spec, err := os.ReadFile(filepath.Join(root, "docs", "isolation-spec.md"))
	if err != nil {
		t.Fatal(err)
	}
	vectors, err := os.ReadFile(filepath.Join(root, "testdata", "isolation", "README.md"))
	if err != nil {
		t.Fatal(err)
	}

	readmeText := string(readme)
	for _, n := range []string{
		"[Isolation spec](docs/isolation-spec.md)",
		"testdata/isolation/",
	} {
		if !strings.Contains(readmeText, n) {
			t.Errorf("README.md missing %q", n)
		}
	}

	specText := string(spec)
	for _, n := range []string{
		"allow_empty",
		"introduced",
		"testdata/isolation/",
		"## Client README contract",
		"https://github.com/barzoj-co/llmentary-go",
		"https://github.com/barzoj-co/llmentary-python",
		"https://github.com/barzoj-co/llmentary-js",
		"[README](../README.md)",
	} {
		if !strings.Contains(specText, n) {
			t.Errorf("docs/isolation-spec.md missing %q", n)
		}
	}

	vecText := string(vectors)
	for _, n := range []string{
		"docs/isolation-spec.md",
		"llmentary-go",
		"llmentary-python",
		"llmentary-js",
		"README.md",
	} {
		if !strings.Contains(vecText, n) {
			t.Errorf("testdata/isolation/README.md missing %q", n)
		}
	}
}
