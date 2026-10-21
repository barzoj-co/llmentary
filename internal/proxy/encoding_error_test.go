// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"bytes"
	"compress/gzip"
	"testing"
)

func TestDecodeOneErrorPathsAndIdentity(t *testing.T) {
	t.Parallel()
	junk := []byte("not-compressed")
	if got := decompressIfNeeded(junk, map[string][]string{"Content-Encoding": {"gzip"}}, nil); !bytes.Equal(got, junk) {
		t.Fatal("bad gzip")
	}
	if got := decompressIfNeeded(junk, map[string][]string{"content-encoding": {"x-gzip"}}, nil); !bytes.Equal(got, junk) {
		t.Fatal("bad x-gzip")
	}
	if got := decompressIfNeeded(junk, map[string][]string{"Content-Encoding": {"deflate"}}, nil); !bytes.Equal(got, junk) {
		t.Fatal("bad deflate")
	}
	if got := decompressIfNeeded(junk, map[string][]string{"Content-Encoding": {"br"}}, nil); !bytes.Equal(got, junk) {
		t.Fatal("bad br")
	}
	if got := decompressIfNeeded(junk, map[string][]string{"Content-Encoding": {"zstd"}}, nil); !bytes.Equal(got, junk) {
		t.Fatal("bad zstd")
	}
	if got := decompressIfNeeded(junk, map[string][]string{"Content-Encoding": {"identity, "}}, nil); !bytes.Equal(got, junk) {
		t.Fatal("identity")
	}

	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write([]byte(`{"ok":true}`))
	_ = zw.Close()
	if got := decompressIfNeeded(buf.Bytes(), map[string][]string{"Content-Encoding": {"x-gzip"}}, nil); string(got) != `{"ok":true}` {
		t.Fatalf("x-gzip %q", got)
	}

	_, ok, known := decodeOne(nil, "nope")
	if ok || known {
		t.Fatal("unknown encoding")
	}
}
