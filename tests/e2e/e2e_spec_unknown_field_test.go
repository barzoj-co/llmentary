// SPDX-License-Identifier: AGPL-3.0-only

//go:build e2e

package llmentary_test

import (
	"strings"
	"testing"
)

func TestLiveSpecUnknownFieldMessage(t *testing.T) {
	apiKey := requireAPIKey(t)
	p := startLiveProxy(t, "warn", "relaxed")
	sid := liveSession()
	body := []byte(`{"model":` + quoteJSON(liveModel()) + `,"messages":[{"role":"user","content":"Reply with ok."}],"max_tokens":16,"temprature":0}`)
	_, _, resp, session := postLive(t, apiKey, p, "/chat/completions", sid, body)
	n := liveNormalized(liveLastCall(t, session))
	found := false
	for _, v := range requestSideViolations(n) {
		msg, _ := v["message"].(string)
		if strings.Contains(msg, `unknown field "temprature"`) {
			found = true
			if strings.Contains(msg, "false schema") {
				t.Fatalf("library wording leaked: %q", msg)
			}
		}
	}
	if !found {
		t.Fatalf("missing unknown-field message: violations=%v body=%s", n["spec_violations"], redactLiveBody(resp))
	}
}
