// SPDX-License-Identifier: AGPL-3.0-only

//go:build e2e

package llmentary_test

import (
	"io"
	"net/http"
	"strconv"
	"testing"
	"time"
)

func TestE2EWaitQuietFromLastActivity(t *testing.T) {
	apiKey := requireAPIKey(t)
	model := e2eModel()
	lm := newE2E(t)
	chatBody := []byte(`{"model":` + strconv.Quote(model) + `,"messages":[{"role":"user","content":"Reply with the single word pong."}],"temperature":0,"max_tokens":16}`)
	_ = postOpenAI(t, apiKey, lm, "/chat/completions", chatBody)
	time.Sleep(time.Second)
	start := time.Now()
	req, err := http.NewRequest(http.MethodGet, lm.ControlURL()+"/sessions/"+lm.SessionID()+"/wait", nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("wait status=%d body=%s", res.StatusCode, raw)
	}
	if elapsed := time.Since(start); elapsed >= 200*time.Millisecond {
		t.Fatalf("wait after 1s idle took %s, want < 200ms", elapsed)
	}
}
