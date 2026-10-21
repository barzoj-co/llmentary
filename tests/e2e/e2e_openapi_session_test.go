// SPDX-License-Identifier: AGPL-3.0-only

//go:build e2e

package llmentary_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/barzoj-co/llmentary/internal/proxy"
	"gopkg.in/yaml.v3"
)

func TestE2EOpenAPISessionSchema(t *testing.T) {
	apiKey := requireAPIKey(t)
	p := startLiveProxy(t, "off", "relaxed")
	res, err := http.Get(p.controlURL + "/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 200 {
		t.Fatalf("openapi status=%d", res.StatusCode)
	}
	vres, err := http.Get(p.controlURL + "/version")
	if err != nil {
		t.Fatal(err)
	}
	vraw, err := io.ReadAll(vres.Body)
	_ = vres.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	var ver struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(vraw, &ver); err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Info struct {
			Version string `yaml:"version"`
		} `yaml:"info"`
		Components struct {
			Schemas map[string]any `yaml:"schemas"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	sessionSchema, _ := spec.Components.Schemas["Session"].(map[string]any)
	if sessionSchema == nil {
		t.Fatal("Session schema missing")
	}
	if ver.Version == "" || spec.Info.Version != ver.Version || ver.Version != proxy.VersionString() {
		t.Fatalf("version skew GET /version=%q info.version=%q VersionString=%q", ver.Version, spec.Info.Version, proxy.VersionString())
	}
	if !strings.Contains(proxy.VersionLine(), ver.Version) {
		t.Fatalf("--version line %q missing %q", proxy.VersionLine(), ver.Version)
	}

	body := []byte(`{"model":` + quoteJSON(liveModel()) + `,"input":"Reply with the single word pong.","max_output_tokens":16}`)
	status, _, resp, session := postLive(t, apiKey, p, "/responses", liveSession(), body)
	if status != 200 {
		t.Fatalf("status=%d body=%s", status, redactLiveBody(resp))
	}
	required, _ := sessionSchema["required"].([]any)
	for _, key := range required {
		name := fmt.Sprint(key)
		if _, ok := session[name]; !ok {
			t.Fatalf("session missing required %s", name)
		}
	}
	if fmt.Sprint(session["id"]) == "" {
		t.Fatal("session id empty")
	}
	if len(liveSlice(session["calls"])) == 0 {
		t.Fatal("session calls empty")
	}
}
