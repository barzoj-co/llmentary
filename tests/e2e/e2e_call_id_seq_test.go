// SPDX-License-Identifier: AGPL-3.0-only

//go:build e2e

package llmentary_test

import (
	"testing"
)

func TestE2ECallIDProcessGlobal(t *testing.T) {
	apiKey := requireAPIKey(t)
	p := startLiveProxy(t, "off", "relaxed")
	a := liveSession()
	b := liveSession()
	body := []byte(`{"model":` + quoteJSON(liveModel()) + `,"input":"Reply with the single word pong.","max_output_tokens":16}`)
	if status, _, resp, _ := postLive(t, apiKey, p, "/responses", a, body); status != 200 {
		t.Fatalf("A1 status=%d body=%s", status, redactLiveBody(resp))
	}
	_, _, _, bSession := postLive(t, apiKey, p, "/responses", b, body)
	if status, _, resp, _ := postLive(t, apiKey, p, "/responses", a, body); status != 200 {
		t.Fatalf("A2 status=%d body=%s", status, redactLiveBody(resp))
	}
	call := liveLastCall(t, bSession)
	if call["seq"] != float64(1) {
		t.Fatalf("B seq=%v want 1 id=%v", call["seq"], call["id"])
	}
	if call["id"] == "call_1" {
		t.Fatalf("B id unexpectedly call_1")
	}
}
