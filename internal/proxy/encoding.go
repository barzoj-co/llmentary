// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"io"
	"log/slog"
	"strings"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

func decompressIfNeeded(body []byte, headers map[string][]string, logger *slog.Logger) []byte {
	encodings := contentEncodings(headers)
	if len(encodings) == 0 {
		return body
	}
	plain := body
	for i := len(encodings) - 1; i >= 0; i-- {
		enc := encodings[i]
		next, ok, known := decodeOne(plain, enc)
		if !known {
			if logger != nil {
				logger.Warn("unknown content encoding", "encoding", enc)
			}
			return body
		}
		if !ok {
			return body
		}
		plain = next
	}
	return plain
}

func contentEncodings(headers map[string][]string) []string {
	var raw []string
	for key, values := range headers {
		if !strings.EqualFold(key, "Content-Encoding") {
			continue
		}
		raw = append(raw, values...)
	}
	var out []string
	for _, item := range raw {
		for _, part := range strings.Split(item, ",") {
			part = strings.ToLower(strings.TrimSpace(part))
			if part == "" || part == "identity" {
				continue
			}
			out = append(out, part)
		}
	}
	return out
}

func decodeOne(body []byte, encoding string) (decoded []byte, ok, known bool) {
	switch encoding {
	case "gzip", "x-gzip":
		r, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil, false, true
		}
		defer func() { _ = r.Close() }()
		plain, err := io.ReadAll(r)
		if err != nil {
			return nil, false, true
		}
		return plain, true, true
	case "deflate":
		if zr, err := zlib.NewReader(bytes.NewReader(body)); err == nil {
			plain, zerr := io.ReadAll(zr)
			_ = zr.Close()
			if zerr == nil {
				return plain, true, true
			}
		}
		r := flate.NewReader(bytes.NewReader(body))
		defer func() { _ = r.Close() }()
		plain, err := io.ReadAll(r)
		if err != nil {
			return nil, false, true
		}
		return plain, true, true
	case "br":
		plain, err := io.ReadAll(brotli.NewReader(bytes.NewReader(body)))
		if err != nil {
			return nil, false, true
		}
		return plain, true, true
	case "zstd":
		r, err := zstd.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil, false, true
		}
		defer r.Close()
		plain, err := io.ReadAll(r)
		if err != nil {
			return nil, false, true
		}
		return plain, true, true
	default:
		return nil, false, false
	}
}
