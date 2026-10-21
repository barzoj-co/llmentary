// SPDX-License-Identifier: AGPL-3.0-only

//go:build e2e

package llmentary_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/barzoj-co/llmentary/internal/isolation"
)

func TestE2EIsolatedFromSamePromptAndReplay(t *testing.T) {
	apiKey := requireAPIKey(t)
	p := startLiveProxy(t, "off", "relaxed")
	model := quoteJSON(liveModel())
	prompt := "What is my biological age?"

	sidA := liveSession()
	sidB := liveSession()
	body := []byte(`{"model":` + model + `,"input":` + quoteJSON(prompt) + `,"max_output_tokens":16}`)
	status, _, resp, sessA := postLive(t, apiKey, p, "/responses", sidA, body)
	if status != 200 {
		t.Fatalf("A status=%d body=%s", status, redactLiveBody(resp))
	}
	status, _, resp, sessB := postLive(t, apiKey, p, "/responses", sidB, body)
	if status != 200 {
		t.Fatalf("B status=%d body=%s", status, redactLiveBody(resp))
	}

	a := sessionFromControl(t, sessA)
	b := sessionFromControl(t, sessB)
	if ok, msg := isolation.Check(a, b, isolation.Options{}); !ok {
		t.Fatalf("A isolated_from B: %s", msg)
	}
	if ok, msg := isolation.Check(b, a, isolation.Options{}); !ok {
		t.Fatalf("B isolated_from A: %s", msg)
	}

	sidC := liveSession()
	replay := []byte(`{"model":` + model + `,"input":[` +
		`{"role":"user","content":` + quoteJSON(prompt) + `},` +
		`{"role":"assistant","content":"I need more data."},` +
		`{"role":"user","content":"Continue from the saved thread."}` +
		`],"max_output_tokens":16}`)
	status, _, resp, sessC := postLive(t, apiKey, p, "/responses", sidC, replay)
	if status != 200 {
		t.Fatalf("C status=%d body=%s", status, redactLiveBody(resp))
	}
	c := sessionFromControl(t, sessC)
	ok, msg := isolation.Check(c, a, isolation.Options{})
	if ok {
		t.Fatal("C isolated_from A: want fail on history replay")
	}
	for _, frag := range []string{"user text", "also introduced by session", prompt, sidA, sidC} {
		if !strings.Contains(msg, frag) {
			t.Fatalf("replay message %q missing %q", msg, frag)
		}
	}

	// DOG-34: B sent the same canned text and never touched A's thread.
	ok, msg = isolation.Check(c, b, isolation.Options{})
	if ok {
		t.Fatal("C isolated_from B: want fail on shared text (B uninvolved)")
	}
	if !strings.Contains(msg, sidB) || !strings.Contains(msg, "also introduced by session") {
		t.Fatalf("uninvolved-B message %q", msg)
	}

	// DOG-32: empty / mistyped other is not a vacuous pass (no extra OpenAI call).
	ghost := isolation.Session{ID: sidA + "-typo"}
	ok, msg = isolation.Check(a, ghost, isolation.Options{})
	if ok {
		t.Fatal("A isolated_from never-created: want fail")
	}
	if !strings.Contains(msg, ghost.ID) || !strings.Contains(msg, "isolated_from needs calls on both sides") {
		t.Fatalf("empty other msg=%q", msg)
	}
	if ok, msg = isolation.Check(a, ghost, isolation.Options{AllowEmpty: true}); !ok {
		t.Fatalf("allow_empty other: %s", msg)
	}
}

func sessionFromControl(t *testing.T, raw map[string]any) isolation.Session {
	t.Helper()
	b, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	var s isolation.Session
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	if s.ID == "" {
		t.Fatal("session id empty")
	}
	return s
}
