// SPDX-License-Identifier: AGPL-3.0-only

package compat_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/barzoj-co/llmentary/internal/proxy"
)

var updateGoldens = flag.Bool("update", false, "rewrite matrix.json and docs/compat-matrix.md")

func TestCompatMatrix(t *testing.T) {
	cases := buildMatrix()
	if len(cases) < minMatrixCases {
		t.Fatalf("matrix has %d cases, want at least %d", len(cases), minMatrixCases)
	}
	root := repoRoot(t)
	jsonPath := filepath.Join("matrix.json")
	docPath := filepath.Join(root, "docs", "compat-matrix.md")
	wantJSON := marshalMatrix(cases)
	wantDoc := renderCompatMatrix(cases)
	if *updateGoldens {
		if err := os.WriteFile(jsonPath, wantJSON, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(docPath, []byte(wantDoc), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gotJSON, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("read matrix.json: %v (run go test -update ./...)", err)
	}
	if !bytes.Equal(gotJSON, wantJSON) {
		t.Fatalf("tests/compat/matrix.json is stale; run go test -update ./...\n%s", goldenDiff(gotJSON, wantJSON))
	}
	gotDoc, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("read docs/compat-matrix.md: %v (run go test -update ./...)", err)
	}
	if !bytes.Equal(gotDoc, []byte(wantDoc)) {
		t.Fatalf("docs/compat-matrix.md is stale; run go test -update ./...\n%s", goldenDiff(gotDoc, []byte(wantDoc)))
	}

	type key struct{ mode, target, profile string }
	proxies := map[key]compatProxy{}
	proxyFor := func(c matrixCase) compatProxy {
		k := key{c.Mode, c.Target, c.Profile}
		if p, ok := proxies[k]; ok {
			return p
		}
		p := startCompatProxy(t, c.Mode, c.Profile, c.Target)
		proxies[k] = p
		return p
	}

	var diffs []string
	for _, c := range cases {
		c := c
		status, call := postCase(t, proxyFor(c), c.Path, newSession(), c.Request)
		if d := verdictDiff(c.ID, status, call, c.Want); d != "" {
			diffs = append(diffs, d)
		}
	}
	if len(diffs) > 0 {
		t.Fatalf("compat matrix verdict diffs (%d):\n%s", len(diffs), strings.Join(diffs, "\n"))
	}
}

func TestCompatMatrixJSONParses(t *testing.T) {
	raw, err := os.ReadFile("matrix.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []matrixCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) < minMatrixCases {
		t.Fatalf("len=%d", len(cases))
	}
	for _, c := range cases {
		if c.Target != proxy.SpecTargetOpenAI && c.Target != proxy.SpecTargetCompatible {
			t.Fatalf("bad target %q", c.Target)
		}
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}

func goldenDiff(got, want []byte) string {
	if bytes.Equal(got, want) {
		return "got and want are identical"
	}
	off := firstDiffByte(got, want)
	line := 1 + bytes.Count(prefix(got, off), []byte("\n"))
	return fmt.Sprintf("first difference at byte %d (line %d)\ngot:\n%s\nwant:\n%s",
		off, line, snippetAround(got, off), snippetAround(want, off))
}

func firstDiffByte(got, want []byte) int {
	n := len(got)
	if len(want) < n {
		n = len(want)
	}
	for i := 0; i < n; i++ {
		if got[i] != want[i] {
			return i
		}
	}
	return n
}

func prefix(b []byte, n int) []byte {
	if n > len(b) {
		n = len(b)
	}
	if n < 0 {
		return nil
	}
	return b[:n]
}

func snippetAround(b []byte, off int) string {
	const window = 160
	if len(b) == 0 {
		return "(empty)"
	}
	if off < 0 {
		off = 0
	}
	if off > len(b) {
		off = len(b)
	}
	start := off - window
	if start < 0 {
		start = 0
	}
	if i := bytes.LastIndex(b[:off], []byte("\n")); i >= 0 && i+1 < off && off-i <= window {
		start = i + 1
	}
	end := off + window
	if end > len(b) {
		end = len(b)
	}
	if i := bytes.Index(b[off:], []byte("\n")); i >= 0 && off+i < end {
		end = off + i
	}
	var bld strings.Builder
	if start > 0 {
		bld.WriteString("…")
	}
	bld.Write(b[start:end])
	if end < len(b) {
		bld.WriteString("…")
	}
	return bld.String()
}
