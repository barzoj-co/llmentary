// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const (
	listSessionsHint     = "list sessions with GET /sessions"
	oldKnownSessionsHint = "known sessions: GET /sessions"
)

func TestControlOpenAPIMatchesRouteTable(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "api", "control", "openapi.yaml"))
	if err != nil {
		t.Fatalf("read openapi: %v", err)
	}
	if err := checkControlOpenAPIDocument(raw); err != nil {
		t.Fatal(err)
	}
}

func TestControlOpenAPISurvivesReleasePleaseYAMLRewrite(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "api", "control", "openapi.yaml"))
	if err != nil {
		t.Fatalf("read openapi: %v", err)
	}
	rewritten := simulateReleasePleaseYAMLRewrite(raw)
	if strings.Contains(string(rewritten), listSessionsHint) {
		t.Fatal("rewrite fixture still has the hint on one line; the test would not catch a raw-text check")
	}
	if !strings.Contains(string(rewritten), "list sessions with GET") || !strings.Contains(string(rewritten), "/sessions") {
		t.Fatalf("rewrite lost the hint pieces:\n%s", rewritten)
	}
	ver, err := OpenAPIInfoVersion(rewritten)
	if err != nil {
		t.Fatal(err)
	}
	if ver != "9.9.9" {
		t.Fatalf("rewritten info.version=%q", ver)
	}
	if err := checkControlOpenAPIDocument(rewritten); err != nil {
		t.Fatal(err)
	}
}

func TestOpenAPIAliasDocsMarkDeprecated(t *testing.T) {
	for _, rel := range []string{
		filepath.Join("..", "..", "docs", "proxy.md"),
		filepath.Join("..", "..", "docs", "session-lifecycle.md"),
	} {
		raw, err := os.ReadFile(rel)
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		for _, n := range []string{"PascalCase", "deprecated", "removed in 1.0"} {
			if !strings.Contains(strings.ToLower(text), strings.ToLower(n)) {
				t.Errorf("%s missing %q", rel, n)
			}
		}
	}
}

func TestOpenAPISpecAliasesAreDeprecated(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "api", "control", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := checkOpenAPISpecAliasesDeprecated(raw); err != nil {
		t.Fatal(err)
	}
	if err := checkOpenAPISpecAliasesDeprecated(simulateReleasePleaseYAMLRewrite(raw)); err != nil {
		t.Fatal(err)
	}
}

func checkOpenAPISpecAliasesDeprecated(raw []byte) error {
	var spec map[string]any
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		return fmt.Errorf("parse openapi: %w", err)
	}
	aliases := map[string]string{
		"Checked":     "checked",
		"Clean":       "clean",
		"Findings":    "findings",
		"RequestOnly": "request_only",
		"NotChecked":  "not_checked",
	}
	props := specProperties(spec, "SessionSummary", "spec")
	if props == nil {
		return fmt.Errorf("SessionSummary.spec properties missing")
	}
	for alias, canonical := range aliases {
		prop, ok := props[alias].(map[string]any)
		if !ok {
			return fmt.Errorf("alias %s missing from OpenAPI spec counters", alias)
		}
		if prop["deprecated"] != true {
			return fmt.Errorf("alias %s is not marked deprecated: true", alias)
		}
		desc, _ := prop["description"].(string)
		if !strings.Contains(desc, "Removed in 1.0") || !strings.Contains(desc, "`"+canonical+"`") {
			return fmt.Errorf("alias %s missing removal note: %q", alias, desc)
		}
		if _, ok := props[canonical]; !ok {
			return fmt.Errorf("canonical %s missing next to alias %s", canonical, alias)
		}
	}
	return nil
}

func specProperties(spec map[string]any, schema, field string) map[string]any {
	components, _ := spec["components"].(map[string]any)
	schemas, _ := components["schemas"].(map[string]any)
	item, _ := schemas[schema].(map[string]any)
	props, _ := item["properties"].(map[string]any)
	nested, _ := props[field].(map[string]any)
	out, _ := nested["properties"].(map[string]any)
	return out
}

func checkControlOpenAPIDocument(raw []byte) error {
	var spec map[string]any
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		return fmt.Errorf("parse openapi: %w", err)
	}
	paths, _ := spec["paths"].(map[string]any)
	if len(paths) == 0 {
		return fmt.Errorf("openapi has no paths")
	}
	seen := map[string]bool{}
	for _, route := range ControlRoutes {
		path := route.Path
		if path == "/sessions/" {
			path = "/sessions"
		}
		pathItem, ok := paths[path].(map[string]any)
		if !ok {
			return fmt.Errorf("route %s %s missing from api/control/openapi.yaml", route.Method, route.Path)
		}
		if _, ok := pathItem[strings.ToLower(route.Method)]; !ok {
			return fmt.Errorf("route %s %s missing from api/control/openapi.yaml", route.Method, route.Path)
		}
		seen[path] = true
	}
	for path := range paths {
		if !seen[path] {
			return fmt.Errorf("openapi path %s is not in ControlRoutes", path)
		}
	}

	var values []string
	collectYAMLStrings(spec, &values)
	foundType, foundHint, foundOld := false, false, false
	for _, s := range values {
		if s == "session_not_found" || strings.Contains(s, "session_not_found") {
			foundType = true
		}
		if strings.Contains(s, listSessionsHint) {
			foundHint = true
		}
		if strings.Contains(s, oldKnownSessionsHint) {
			foundOld = true
		}
	}
	if !foundType {
		return fmt.Errorf("openapi must document session_not_found")
	}
	if !foundHint {
		return fmt.Errorf("openapi example must use the list-sessions hint")
	}
	if foundOld {
		return fmt.Errorf("openapi still has the old known-sessions hint")
	}
	return nil
}

func collectYAMLStrings(v any, out *[]string) {
	switch t := v.(type) {
	case string:
		*out = append(*out, t)
	case map[string]any:
		for _, child := range t {
			collectYAMLStrings(child, out)
		}
	case []any:
		for _, child := range t {
			collectYAMLStrings(child, out)
		}
	}
}

var infoVersionLine = regexp.MustCompile(`(?m)^  version: .+\n`)

// simulateReleasePleaseYAMLRewrite copies the yaml extra-files updater
// failure from release-please PR #46: bump info.version and fold long
// scalars so a raw strings.Contains check on the 404 hint no longer matches.
func simulateReleasePleaseYAMLRewrite(src []byte) []byte {
	s := string(src)
	if loc := infoVersionLine.FindStringIndex(s); loc != nil {
		s = s[:loc[0]] + "  version: 9.9.9\n" + s[loc[1]:]
	}
	old := `message: 'no calls recorded for session "ghost"; list sessions with GET /sessions'`
	folded := "message: >-\n                no calls recorded for session \"ghost\"; list sessions with GET\n                /sessions"
	if !strings.Contains(s, old) {
		return []byte(s)
	}
	return []byte(strings.Replace(s, old, folded, 1))
}
