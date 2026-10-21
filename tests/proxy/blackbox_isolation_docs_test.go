// SPDX-License-Identifier: AGPL-3.0-only

package proxy_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBlackBoxIsolationDocs(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "black-box.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	needles := []string{
		"What a path prefix isolates",
		"One worker process per session",
		"Construct the SDK client per conversation",
		"Headers or baggage from the app",
		"Run conversations sequentially",
		"Two consumer groups on one topic",
		"Proving isolation",
		"IsolatedFrom",
		"isolated_from",
		"isolatedFrom",
	}
	for _, n := range needles {
		if !strings.Contains(text, n) {
			t.Errorf("docs/black-box.md missing %q", n)
		}
	}
}
