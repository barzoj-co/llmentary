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
	if strings.Contains(text, "]("+dest+")") {
		return true
	}
	prefix := "](" + dest + "#"
	start := 0
	for {
		i := strings.Index(text[start:], prefix)
		if i < 0 {
			return false
		}
		i += start
		rest := text[i+len(prefix):]
		j := strings.IndexByte(rest, ')')
		if j >= 0 && !strings.ContainsRune(rest[:j], '\n') {
			return true
		}
		start = i + len(prefix)
	}
}

func stripMarkdownDest(text, dest string) string {
	text = strings.ReplaceAll(text, "]("+dest+")", dest)
	prefix := "](" + dest + "#"
	for {
		i := strings.Index(text, prefix)
		if i < 0 {
			return text
		}
		rest := text[i+len(prefix):]
		j := strings.IndexByte(rest, ')')
		if j < 0 {
			return text
		}
		text = text[:i] + dest + rest[j+1:]
	}
}

func TestCheckReadmeRequiresIsolationMarkdownDests(t *testing.T) {
	root := filepath.Join("..", "..")
	script, err := os.ReadFile(filepath.Join(root, "scripts", "check-readme.sh"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(script)
	for _, needle := range []string{
		`](../testdata/isolation/README.md)`,
		`](../README.md)`,
		`](../../docs/isolation-spec.md)`,
		`](../../README.md)`,
		`allow_empty`,
		`## Client README contract`,
		`def has_markdown_dest`,
		`has_markdown_dest(spec, needle)`,
		`has_markdown_dest(vec, needle)`,
		`rest.find(")")`,
	} {
		if !strings.Contains(text, needle) {
			t.Errorf("check-readme.sh missing required needle %q", needle)
		}
	}
	// Bare path text must not be enough for the spec/vector link checks.
	// Keep the README required_snippets (already markdown dests) intact.
	if strings.Contains(text, `"testdata/isolation/",`) {
		t.Fatal("spec check still accepts a bare testdata/isolation/ path")
	}
	if strings.Contains(text, `"docs/isolation-spec.md",`) {
		t.Fatal("vectors check still accepts a bare docs/isolation-spec.md path")
	}

	spec, err := os.ReadFile(filepath.Join(root, "docs", "isolation-spec.md"))
	if err != nil {
		t.Fatal(err)
	}
	stripped := stripMarkdownDest(string(spec), "../testdata/isolation/README.md")
	if !strings.Contains(stripped, "testdata/isolation/README.md") && !strings.Contains(stripped, "../testdata/isolation/README.md") {
		t.Fatal("expected dest path after stripping markdown")
	}
	if hasMarkdownDest(stripped, "../testdata/isolation/README.md") {
		t.Fatal("stripped spec still has markdown dest")
	}

	vec, err := os.ReadFile(filepath.Join(root, "testdata", "isolation", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	strippedVec := stripMarkdownDest(string(vec), "../../docs/isolation-spec.md")
	if hasMarkdownDest(strippedVec, "../../docs/isolation-spec.md") {
		t.Fatal("stripped vectors still have markdown dest")
	}
	// Hash-suffix dest still counts as a link; unclosed fragment and bare path do not.
	if !hasMarkdownDest("[spec](../../docs/isolation-spec.md#verdict)", "../../docs/isolation-spec.md") {
		t.Fatal("hash suffix dest should still count")
	}
	if hasMarkdownDest("[spec](../../docs/isolation-spec.md#verdict", "../../docs/isolation-spec.md") {
		t.Fatal("unclosed fragment must not count")
	}
	if hasMarkdownDest("see docs/isolation-spec.md", "../../docs/isolation-spec.md") {
		t.Fatal("bare prose path must not count as a dest")
	}
	if hasMarkdownDest(stripMarkdownDest("[x](../README.md#top)", "../README.md"), "../README.md") {
		t.Fatal("stripped fragment dest still matches")
	}
}
