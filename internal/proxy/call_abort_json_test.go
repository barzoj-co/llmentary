// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"encoding/json"
	"testing"
)

func TestCallJSONClientAbortedTopLevel(t *testing.T) {
	aborted := Call{
		ID:            "call_1",
		SessionID:     "barge",
		Seq:           1,
		HTTPStatus:    0,
		ClientAborted: true,
		AbortPhase:    AbortPhaseBeforeHeaders,
		Normalized: Normalized{
			ClientAborted: true,
			FinishReason:  "cancelled",
		},
	}
	raw, err := json.Marshal(aborted)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m["client_aborted"] != true {
		t.Fatalf("top-level=%v body=%s", m["client_aborted"], raw)
	}
	if m["abort_phase"] != AbortPhaseBeforeHeaders {
		t.Fatalf("abort_phase=%v", m["abort_phase"])
	}
	n, _ := m["normalized"].(map[string]any)
	if n["client_aborted"] != true {
		t.Fatalf("alias missing: %v", n)
	}

	normal := Call{ID: "call_2", HTTPStatus: 200, Normalized: Normalized{FinishReason: "stop"}}
	raw, err = json.Marshal(normal)
	if err != nil {
		t.Fatal(err)
	}
	m = map[string]any{}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m["client_aborted"] != false {
		t.Fatalf("normal client_aborted=%v", m["client_aborted"])
	}
	if _, ok := m["abort_phase"]; ok {
		t.Fatalf("normal abort_phase present: %v", m["abort_phase"])
	}
}
