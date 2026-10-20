// SPDX-License-Identifier: AGPL-3.0-only

package tracetest

import (
	"net/url"
	"testing"
)

func TestClientResolveDoesNotDoubleEscape(t *testing.T) {
	t.Parallel()
	c, err := NewClient("http://127.0.0.1:9")
	if err != nil {
		t.Fatal(err)
	}
	got := c.resolve("/sessions/" + url.PathEscape("50%") + "/wait")
	if got.EscapedPath() != "/sessions/50%25/wait" {
		t.Fatalf("escaped=%q", got.EscapedPath())
	}
	if got.Path != "/sessions/50%/wait" {
		t.Fatalf("path=%q", got.Path)
	}
}
