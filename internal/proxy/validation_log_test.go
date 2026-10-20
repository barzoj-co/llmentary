// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"strings"
	"testing"

	"github.com/barzoj-co/llmentary/internal/specvalidate"
)

func TestWarnViolationMessageHidesFormatValue(t *testing.T) {
	t.Parallel()
	got := warnViolationMessage(specvalidate.Violation{
		Keyword: "format",
		Message: `does not match format 'uri': "https://secret.example/img.png?token=abc"`,
	})
	if got != "format validation failed" {
		t.Fatalf("got %q", got)
	}
	if strings.Contains(got, "secret") || strings.Contains(got, "token") {
		t.Fatalf("leaked value: %q", got)
	}
	other := warnViolationMessage(specvalidate.Violation{Keyword: "required", Message: "missing property messages"})
	if other != "missing property messages" {
		t.Fatalf("non-format message changed: %q", other)
	}
}
