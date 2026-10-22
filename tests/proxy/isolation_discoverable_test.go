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
	for _, dest := range []string{
		"docs/isolation-spec.md",
		"testdata/isolation/",
	} {
		if !hasMarkdownDest(readmeText, dest) {
			t.Errorf("README.md missing markdown link to %q", dest)
		}
	}

	specText := string(spec)
	for _, n := range []string{
		"allow_empty",
		"introduced",
		"## Client README contract",
		"https://github.com/barzoj-co/llmentary-go",
		"https://github.com/barzoj-co/llmentary-python",
		"https://github.com/barzoj-co/llmentary-js",
	} {
		if !strings.Contains(specText, n) {
			t.Errorf("docs/isolation-spec.md missing %q", n)
		}
	}
	for _, dest := range []string{
		"../testdata/isolation/README.md",
		"../README.md",
	} {
		if !hasMarkdownDest(specText, dest) {
			t.Errorf("docs/isolation-spec.md missing markdown link to %q", dest)
		}
	}

	vecText := string(vectors)
	for _, dest := range []string{
		"../../docs/isolation-spec.md",
		"../../README.md",
		"https://github.com/barzoj-co/llmentary-go",
		"https://github.com/barzoj-co/llmentary-python",
		"https://github.com/barzoj-co/llmentary-js",
	} {
		if !hasMarkdownDest(vecText, dest) {
			t.Errorf("testdata/isolation/README.md missing markdown link to %q", dest)
		}
	}
}

func hasMarkdownDest(text, dest string) bool {
	return strings.Contains(text, "]("+dest+")") || strings.Contains(text, "]("+dest+"#")
}
