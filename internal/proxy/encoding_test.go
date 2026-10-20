// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

func newTextLogger(w io.Writer) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

func TestDecompressGzip(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write([]byte(`{"ok":true}`))
	_ = zw.Close()
	headers := map[string][]string{"Content-Encoding": {"gzip"}}
	got := decompressIfNeeded(buf.Bytes(), headers, nil)
	if string(got) != `{"ok":true}` {
		t.Fatalf("got %q", got)
	}
}

func TestDecompressDeflateBrotliZstd(t *testing.T) {
	t.Parallel()
	plain := []byte(`{"ok":true}`)

	var deflateBuf bytes.Buffer
	zw := zlib.NewWriter(&deflateBuf)
	_, _ = zw.Write(plain)
	_ = zw.Close()
	if got := decompressIfNeeded(deflateBuf.Bytes(), map[string][]string{"Content-Encoding": {"deflate"}}, nil); !bytes.Equal(got, plain) {
		t.Fatalf("deflate got %q", got)
	}

	var brBuf bytes.Buffer
	bw := brotli.NewWriter(&brBuf)
	_, _ = bw.Write(plain)
	_ = bw.Close()
	if got := decompressIfNeeded(brBuf.Bytes(), map[string][]string{"Content-Encoding": {"br"}}, nil); !bytes.Equal(got, plain) {
		t.Fatalf("br got %q", got)
	}

	var zstdBuf bytes.Buffer
	zwstd, err := zstd.NewWriter(&zstdBuf)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = zwstd.Write(plain)
	_ = zwstd.Close()
	if got := decompressIfNeeded(zstdBuf.Bytes(), map[string][]string{"Content-Encoding": {"zstd"}}, nil); !bytes.Equal(got, plain) {
		t.Fatalf("zstd got %q", got)
	}
}

func TestDecompressUnknownWarns(t *testing.T) {
	t.Parallel()
	var logBuf bytes.Buffer
	logger := newTextLogger(&logBuf)
	raw := []byte("not-really-compressed")
	got := decompressIfNeeded(raw, map[string][]string{"Content-Encoding": {"weird"}}, logger)
	if !bytes.Equal(got, raw) {
		t.Fatal("unknown encoding should keep raw bytes")
	}
	if !strings.Contains(logBuf.String(), "unknown content encoding") {
		t.Fatalf("log=%s", logBuf.String())
	}
}

func TestDecompressUnknownEmptyBodyWarns(t *testing.T) {
	t.Parallel()
	var logBuf bytes.Buffer
	logger := newTextLogger(&logBuf)
	got := decompressIfNeeded(nil, map[string][]string{"Content-Encoding": {"weird"}}, logger)
	if len(got) != 0 {
		t.Fatalf("got %q", got)
	}
	if !strings.Contains(logBuf.String(), "unknown content encoding") {
		t.Fatalf("log=%s", logBuf.String())
	}
}
