// SPDX-License-Identifier: AGPL-3.0-only

package compat_test

import (
	"strings"
	"testing"
)

func TestSpecClassOffAndEmpty(t *testing.T) {
	t.Parallel()
	if specClass(nil) != "off" {
		t.Fatal("nil call")
	}
	if specClass(map[string]any{}) != "off" {
		t.Fatal("missing normalized")
	}
	if specClass(map[string]any{"normalized": map[string]any{}}) != "off" {
		t.Fatal("missing validation")
	}
	if specClass(map[string]any{
		"normalized": map[string]any{"validation": map[string]any{"mode": "warn"}},
	}) != "clean" {
		t.Fatal("no violations is clean")
	}
	if specClass(map[string]any{
		"normalized": map[string]any{
			"validation":      map[string]any{"mode": "warn"},
			"spec_violations": []any{},
		},
	}) != "clean" {
		t.Fatal("empty violations is clean")
	}
}

func TestSpecClassIgnoresResponseShapeAndInfo(t *testing.T) {
	t.Parallel()
	call := map[string]any{
		"normalized": map[string]any{
			"validation": map[string]any{"mode": "warn", "target": "openai-compatible"},
			"spec_violations": []any{
				map[string]any{"keyword": "response_shape", "severity": "info", "pointer": "/choices/0/finish_reason", "count": float64(3)},
				map[string]any{"keyword": "additionalProperties", "severity": "info", "pointer": "/x_groq"},
				map[string]any{"keyword": "truncated", "severity": "info", "count": float64(9)},
				map[string]any{"severity": "info", "keyword": "enum"},
				nil,
				"skip",
			},
		},
	}
	if specClass(call) != "clean" {
		t.Fatalf("info/response_shape counted: %s", specClass(call))
	}
}

func TestSpecClassCountsNonInfoFindings(t *testing.T) {
	t.Parallel()
	base := func(vs ...any) map[string]any {
		return map[string]any{
			"normalized": map[string]any{
				"validation":      map[string]any{"mode": "warn"},
				"spec_violations": vs,
			},
		}
	}
	if specClass(base(map[string]any{"keyword": "enum", "severity": "warning", "count": float64(2)})) != "findings" {
		t.Fatal("warning count")
	}
	if specClass(base(map[string]any{"keyword": "type", "severity": "error"})) != "findings" {
		t.Fatal("error")
	}
	if specClass(base(
		map[string]any{"keyword": "response_shape", "severity": "info", "count": float64(12)},
		map[string]any{"keyword": "enum", "severity": "warning", "side": "request"},
	)) != "findings" {
		t.Fatal("mixed request warning + response_shape")
	}
	if specClass(base(map[string]any{"keyword": "truncated", "severity": "warning", "count": float64(4)})) != "findings" {
		t.Fatal("openai truncated overflow still counts")
	}
	if specClass(base(map[string]any{"keyword": "response_shape", "severity": "warning"})) != "clean" {
		t.Fatal("response_shape keyword wins over severity")
	}
}

func TestVerdictDiffReportsSpecMismatch(t *testing.T) {
	t.Parallel()
	call := map[string]any{
		"upstream_called": true,
		"normalized": map[string]any{
			"validation":      map[string]any{"mode": "warn"},
			"spec_violations": []any{map[string]any{"keyword": "response_shape", "severity": "info"}},
		},
	}
	if d := verdictDiff("id", 200, call, matrixWant{Status: 200, UpstreamCalled: true, Spec: "clean"}); d != "" {
		t.Fatalf("clean match: %s", d)
	}
	d := verdictDiff("row", 200, call, matrixWant{Status: 200, UpstreamCalled: true, Spec: "findings"})
	if d == "" || !strings.Contains(d, "row") || !strings.Contains(d, "spec: got clean want findings") {
		t.Fatalf("diff: %q", d)
	}
	d = verdictDiff("row", 400, call, matrixWant{Status: 200, UpstreamCalled: false, Spec: "clean"})
	if !strings.Contains(d, "status:") || !strings.Contains(d, "upstream_called:") {
		t.Fatalf("status/called: %q", d)
	}
}
