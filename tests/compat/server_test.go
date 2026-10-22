// SPDX-License-Identifier: AGPL-3.0-only

package compat_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

type fixtureServer struct{}

func (f fixtureServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	_ = r.Body.Close()
	provider, surface := routeFixture(r.URL.Path, raw)
	ct, body := responseFor(provider, surface)
	w.Header().Set("Content-Type", ct)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func routeFixture(path string, raw []byte) (provider, surface string) {
	provider = "fixture"
	var body map[string]any
	if json.Unmarshal(raw, &body) == nil {
		if model, _ := body["model"].(string); strings.HasPrefix(model, "compat-") {
			provider = strings.TrimPrefix(model, "compat-")
		}
	}
	switch {
	case strings.HasSuffix(path, "/responses"):
		if isStream(body) {
			return provider, "responses.sse"
		}
		return provider, "responses.json"
	case strings.Contains(string(raw), `"tool_call_id"`):
		return provider, "chat.tool_result"
	case strings.Contains(string(raw), `"tools"`):
		return provider, "chat.tools"
	case isStream(body):
		return provider, "chat.sse"
	default:
		return provider, "chat.json"
	}
}

func isStream(body map[string]any) bool {
	v, _ := body["stream"].(bool)
	return v
}
