// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestControlOpenAPIMatchesRouteTable(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "api", "control", "openapi.yaml"))
	if err != nil {
		t.Fatalf("read openapi: %v", err)
	}
	var spec struct {
		Paths map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("parse openapi: %v", err)
	}
	if len(spec.Paths) == 0 {
		t.Fatal("openapi has no paths")
	}
	seen := map[string]bool{}
	for _, route := range ControlRoutes {
		path := route.Path
		if path == "/sessions/" {
			path = "/sessions"
		}
		if _, ok := spec.Paths[path]; !ok {
			t.Errorf("route %s %s missing from api/control/openapi.yaml", route.Method, route.Path)
		}
		seen[path] = true
	}
	for path := range spec.Paths {
		if !seen[path] {
			t.Errorf("openapi path %s is not in ControlRoutes", path)
		}
	}
	if !strings.Contains(string(raw), "session_not_found") {
		t.Fatal("openapi must document session_not_found")
	}
}
