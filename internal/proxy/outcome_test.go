// SPDX-License-Identifier: AGPL-3.0-only

package proxy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type outcomeCase struct {
	Name       string          `json:"name"`
	Outcome    string          `json:"outcome"`
	ClientOnly bool            `json:"client_only"`
	Call       json.RawMessage `json:"call"`
}

func TestClassifyOutcomeVector(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", "..", "api", "control", "testdata", "call_outcomes.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Cases []outcomeCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	for _, tc := range doc.Cases {
		if tc.ClientOnly {
			continue
		}
		t.Run(tc.Name, func(t *testing.T) {
			var call Call
			if err := json.Unmarshal(tc.Call, &call); err != nil {
				t.Fatal(err)
			}
			if got := classifyOutcome(call); got != tc.Outcome {
				t.Fatalf("classifyOutcome = %q, want %q", got, tc.Outcome)
			}
		})
	}
}

func TestClassifyOutcomeInFlight(t *testing.T) {
	t.Parallel()
	call := Call{ID: "call_inflight", StartedAt: time.Now().Add(-time.Second)}
	if got := classifyOutcome(call); got != "" {
		t.Fatalf("classifyOutcome = %q, want empty for in-flight call", got)
	}
}
