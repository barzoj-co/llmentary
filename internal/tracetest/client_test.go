// SPDX-License-Identifier: AGPL-3.0-only

package tracetest

import (
	"net/url"
	"testing"
)

func TestClientResolveDoesNotDoubleEscape(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		base, wantPath, wantEscaped string
	}{
		{base: "http://127.0.0.1:9", wantPath: "/sessions/50%/wait", wantEscaped: "/sessions/50%25/wait"},
		{base: "http://127.0.0.1:9/prefix", wantPath: "/prefix/sessions/50%/wait", wantEscaped: "/prefix/sessions/50%25/wait"},
	} {
		t.Run(tc.base, func(t *testing.T) {
			t.Parallel()
			c, err := NewClient(tc.base)
			if err != nil {
				t.Fatal(err)
			}
			got := c.resolve("/sessions/" + url.PathEscape("50%") + "/wait")
			if got.EscapedPath() != tc.wantEscaped {
				t.Fatalf("escaped=%q", got.EscapedPath())
			}
			if got.Path != tc.wantPath {
				t.Fatalf("path=%q", got.Path)
			}
		})
	}
}
