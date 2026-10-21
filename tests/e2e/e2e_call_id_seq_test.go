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
	status, _, resp, a1Session := postLive(t, apiKey, p, "/responses", a, body)
	if status != 200 {
		t.Fatalf("A1 status=%d body=%s", status, redactLiveBody(resp))
	}
	_, _, _, bSession := postLive(t, apiKey, p, "/responses", b, body)
	status, _, resp, a2Session := postLive(t, apiKey, p, "/responses", a, body)
	if status != 200 {
		t.Fatalf("A2 status=%d body=%s", status, redactLiveBody(resp))
	}
	a1Call := liveLastCall(t, a1Session)
	bCall := liveLastCall(t, bSession)
	a2Call := liveLastCall(t, a2Session)
	a1ID, _ := a1Call["id"].(string)
	bID, _ := bCall["id"].(string)
	a2ID, _ := a2Call["id"].(string)
	if a1ID == "" || bID == "" || a2ID == "" {
		t.Fatalf("empty id A1=%q B=%q A2=%q", a1ID, bID, a2ID)
	}
	if a1Call["seq"] != float64(1) {
		t.Fatalf("A1 seq=%v want 1 id=%v", a1Call["seq"], a1ID)
	}
	if bCall["seq"] != float64(1) {
		t.Fatalf("B seq=%v want 1 id=%v", bCall["seq"], bID)
	}
	if a2Call["seq"] != float64(2) {
		t.Fatalf("A2 seq=%v want 2 id=%v", a2Call["seq"], a2ID)
	}
	if bID == "call_1" {
		t.Fatalf("B id unexpectedly call_1")
	}
	if a1ID == bID || a1ID == a2ID || bID == a2ID {
		t.Fatalf("call IDs are not distinct: A1=%q B=%q A2=%q", a1ID, bID, a2ID)
	}
}
