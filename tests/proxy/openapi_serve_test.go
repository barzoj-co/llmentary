// SPDX-License-Identifier: AGPL-3.0-only

package proxy_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/barzoj-co/llmentary/internal/proxy"
	"gopkg.in/yaml.v3"
)

func TestGetOpenAPIYAML(t *testing.T) {
	_, control, _ := startControl(t)
	res, err := http.Get(control + "/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", res.StatusCode)
	}
	if !strings.Contains(res.Header.Get("Content-Type"), "yaml") {
		t.Fatalf("content-type=%q", res.Header.Get("Content-Type"))
	}
	want, err := os.ReadFile(filepath.Join("..", "..", "api", "control", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("served openapi.yaml differs from repo file")
	}
	var spec struct {
		OpenAPI string         `yaml:"openapi"`
		Paths   map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(got, &spec); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(spec.OpenAPI, "3.") {
		t.Fatalf("openapi=%q", spec.OpenAPI)
	}
	if _, ok := spec.Paths["/sessions/{id}/wait"]; !ok {
		t.Fatal("missing wait path")
	}

	tokCfg := proxy.Config{ListenAddr: ":0", ControlAddr: "127.0.0.1:0", ControlToken: "secret"}
	if err := tokCfg.AddUpstream("default=http://example.test"); err != nil {
		t.Fatal(err)
	}
	tokServer, err := proxy.NewServer(tokCfg, proxy.NewMemoryStore(), proxy.TolerantParser{})
	if err != nil {
		t.Fatal(err)
	}
	tok := httptest.NewServer(tokServer.ControlHandler())
	t.Cleanup(tok.Close)
	unauth, err := http.Get(tok.URL + "/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	_ = unauth.Body.Close()
	if unauth.StatusCode != http.StatusUnauthorized {
		t.Fatalf("tokenless status=%d", unauth.StatusCode)
	}
	req, err := http.NewRequest(http.MethodGet, tok.URL+"/openapi.yaml", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer secret")
	auth, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = auth.Body.Close()
	if auth.StatusCode != http.StatusOK {
		t.Fatalf("authed status=%d", auth.StatusCode)
	}
}

func TestUnknownControlPathMentionsOpenAPI(t *testing.T) {
	_, control, _ := startControl(t)
	status, _, body := getJSON(t, control+"/nope")
	if status != http.StatusNotFound {
		t.Fatalf("status=%d", status)
	}
	errObj, _ := body["error"].(map[string]any)
	if !strings.Contains(fmtString(errObj["message"]), "/openapi.yaml") {
		t.Fatalf("message=%v", errObj["message"])
	}
}

func TestSessionLifecycleTableInDocs(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "proxy.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	needles := []string{
		"## Session lifecycle",
		"Never created",
		"Has calls",
		"Deleted",
		"session_not_found",
		"wait_timeout",
		"`GET /sessions/{id}`",
		"`DELETE`",
	}
	for _, n := range needles {
		if !strings.Contains(text, n) {
			t.Errorf("docs/proxy.md missing %q", n)
		}
	}
}

func TestSessionLifecycleHandlerMatrix(t *testing.T) {
	proxyURL, control, _ := startControl(t)
	status, _, _ := getJSON(t, control+"/sessions/ghost-life")
	if status != http.StatusNotFound {
		t.Fatalf("never-created GET=%d", status)
	}
	del, err := http.NewRequest(http.MethodDelete, control+"/sessions/ghost-life", nil)
	if err != nil {
		t.Fatal(err)
	}
	dres, err := http.DefaultClient.Do(del)
	if err != nil {
		t.Fatal(err)
	}
	_ = dres.Body.Close()
	if dres.StatusCode != http.StatusNoContent {
		t.Fatalf("never-created DELETE=%d", dres.StatusCode)
	}
	_, _, listed := getJSON(t, control+"/sessions")
	for _, raw := range asAnySlice(listed["sessions"]) {
		m, _ := raw.(map[string]any)
		if fmtString(m["id"]) == "ghost-life" {
			t.Fatal("never-created id listed")
		}
	}

	postCall(t, proxyURL, "alive")
	status, _, body := getJSON(t, control+"/sessions/alive")
	if status != http.StatusOK || fmtString(body["id"]) != "alive" {
		t.Fatalf("has-calls GET=%d %v", status, body)
	}
	status, _, body = getJSON(t, control+"/sessions/alive/wait?quiet=20ms&timeout=2s&min_calls=0")
	if status != http.StatusOK {
		t.Fatalf("has-calls wait=%d %v", status, body)
	}
	del2, err := http.NewRequest(http.MethodDelete, control+"/sessions/alive", nil)
	if err != nil {
		t.Fatal(err)
	}
	d2, err := http.DefaultClient.Do(del2)
	if err != nil {
		t.Fatal(err)
	}
	_ = d2.Body.Close()
	if d2.StatusCode != http.StatusNoContent {
		t.Fatalf("delete known=%d", d2.StatusCode)
	}
	status, _, _ = getJSON(t, control+"/sessions/alive")
	if status != http.StatusNotFound {
		t.Fatalf("deleted GET=%d", status)
	}
}
