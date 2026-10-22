// SPDX-License-Identifier: AGPL-3.0-only

package compat_test

import (
	"bytes"
	"strings"
	"testing"
)

func TestGoldenDiffIdentical(t *testing.T) {
	t.Parallel()
	got := goldenDiff([]byte("same"), []byte("same"))
	if !strings.Contains(got, "identical") {
		t.Fatalf("identical: %q", got)
	}
}

func TestGoldenDiffShowsFirstChangeNotPrefix(t *testing.T) {
	t.Parallel()
	prefix := bytes.Repeat([]byte("a"), 900)
	got := append(append([]byte{}, prefix...), []byte("GOT-ROW\n")...)
	want := append(append([]byte{}, prefix...), []byte("WANT-ROW\n")...)
	diff := goldenDiff(got, want)
	if strings.Contains(diff, "got prefix:") {
		t.Fatalf("still prints prefixes: %s", diff)
	}
	if !strings.Contains(diff, "byte 900") {
		t.Fatalf("missing first-byte offset: %s", diff)
	}
	if !strings.Contains(diff, "GOT-ROW") || !strings.Contains(diff, "WANT-ROW") {
		t.Fatalf("missing differing text: %s", diff)
	}
	if strings.Contains(diff, string(bytes.Repeat([]byte("a"), 400))) {
		t.Fatalf("dumped identical prefix: %s", diff)
	}
}

func TestGoldenDiffLineAndEmpty(t *testing.T) {
	t.Parallel()
	got := []byte("one\ntwo\nthree\n")
	want := []byte("one\ntwo\nFOUR\n")
	diff := goldenDiff(got, want)
	if !strings.Contains(diff, "line 3") {
		t.Fatalf("line: %s", diff)
	}
	if !strings.Contains(diff, "three") || !strings.Contains(diff, "FOUR") {
		t.Fatalf("context: %s", diff)
	}

	empty := goldenDiff(nil, []byte("x"))
	if !strings.Contains(empty, "(empty)") || !strings.Contains(empty, "byte 0") {
		t.Fatalf("empty got: %s", empty)
	}
	if firstDiffByte([]byte("abc"), []byte("abcXYZ")) != 3 {
		t.Fatal("suffix length")
	}
}
