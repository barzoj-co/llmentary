// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"bytes"
	"fmt"
	"strings"
)

// StampOpenAPIVersion rewrites info.version in a control OpenAPI document to
// the binary version. The first `version:` line under `info:` is replaced;
// `openapi: 3.x` is left alone. Dev builds (`devel`) still produce a valid
// spec. Serve-time stamping is the source of truth so --version, GET /version,
// and GET /openapi.yaml always agree even if the packaged file lags a release.
func StampOpenAPIVersion(src []byte, version string) []byte {
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")
	if version == "" {
		version = "devel"
	}
	idx := bytes.Index(src, []byte("\ninfo:"))
	if idx < 0 {
		if bytes.HasPrefix(src, []byte("info:")) {
			idx = 0
		} else {
			return src
		}
	}
	rest := src[idx:]
	lineStart := bytes.Index(rest, []byte("\n  version:"))
	if lineStart < 0 {
		return src
	}
	lineStart += idx + 1
	lineEnd := bytes.IndexByte(src[lineStart:], '\n')
	if lineEnd < 0 {
		lineEnd = len(src) - lineStart
	}
	out := append([]byte(nil), src[:lineStart]...)
	out = append(out, []byte("  version: "+version)...)
	out = append(out, src[lineStart+lineEnd:]...)
	return out
}

// OpenAPIInfoVersion returns the info.version string from a control spec.
func OpenAPIInfoVersion(src []byte) (string, error) {
	var ver string
	inInfo := false
	for _, line := range strings.Split(string(src), "\n") {
		trim := strings.TrimRight(line, "\r")
		if trim == "info:" {
			inInfo = true
			continue
		}
		if inInfo {
			if strings.HasPrefix(trim, "  version:") {
				ver = strings.TrimSpace(strings.TrimPrefix(trim, "  version:"))
				ver = strings.Trim(ver, `"'`)
				return ver, nil
			}
			if len(trim) > 0 && !strings.HasPrefix(trim, " ") && !strings.HasPrefix(trim, "\t") {
				break
			}
		}
	}
	if ver == "" {
		return "", fmt.Errorf("openapi info.version not found")
	}
	return ver, nil
}

// CheckOpenAPIVersion reports whether info.version equals want (release or
// ldflags version). Used by tests to fail on skew.
func CheckOpenAPIVersion(src []byte, want string) error {
	got, err := OpenAPIInfoVersion(src)
	if err != nil {
		return err
	}
	want = strings.TrimPrefix(strings.TrimSpace(want), "v")
	if got != want {
		return fmt.Errorf("openapi info.version=%q want %q", got, want)
	}
	return nil
}
