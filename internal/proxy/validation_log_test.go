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

func TestWarnViolationMessageHidesNumericAndPatternValues(t *testing.T) {
	t.Parallel()
	for _, kw := range []string{"pattern", "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf"} {
		got := warnViolationMessage(specvalidate.Violation{
			Keyword: kw,
			Message: kw + ` failed: got 99 expected <= 2`,
		})
		if got != "schema validation failed" {
			t.Fatalf("%s: got %q", kw, got)
		}
		if strings.Contains(got, "99") || strings.Contains(got, "2") {
			t.Fatalf("%s leaked value: %q", kw, got)
		}
	}
}
