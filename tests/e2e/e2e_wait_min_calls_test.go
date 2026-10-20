// SPDX-License-Identifier: AGPL-3.0-only

//go:build e2e

package llmentary_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"testing"
	"time"
)

func TestE2EWaitMinCallsAsync(t *testing.T) {
	apiKey := requireAPIKey(t)
	model := e2eModel()
	lm := newE2E(t)
	session := lm.SessionID()

	done := make(chan struct{})
	go func() {
		defer close(done)
		time.Sleep(1500 * time.Millisecond)
		chatBody := []byte(`{"model":` + strconv.Quote(model) + `,"messages":[{"role":"user","content":"Reply with the single word pong."}],"stream":true,"stream_options":{"include_usage":true},"temperature":0,"max_tokens":16}`)
		status, err := postOpenAIStatusErr(t, apiKey, lm, "/chat/completions", chatBody)
		if err != nil {
			t.Errorf("chat stream: %v", err)
			return
		}
		if status != http.StatusOK {
			t.Errorf("chat stream status=%d", status)
		}
		respBody := []byte(`{"model":` + strconv.Quote(model) + `,"input":"Reply with the single word pong.","stream":false,"temperature":0}`)
		status, err = postOpenAIStatusErr(t, apiKey, lm, "/responses", respBody)
		if err != nil {
			t.Errorf("responses: %v", err)
			return
		}
		if status != http.StatusOK {
			t.Errorf("responses status=%d", status)
		}
	}()
	t.Cleanup(func() { <-done })

	req, err := http.NewRequest(http.MethodGet, lm.ControlURL()+"/sessions/"+session+"/wait?min_calls=2&quiet=100ms&timeout=30s", nil)
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
	var body struct {
		Calls []struct {
			Normalized struct {
				Usage *struct {
					TotalTokens *int `json:"total_tokens"`
				} `json:"usage"`
			} `json:"normalized"`
		} `json:"calls"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("wait json: %v body=%s", err, raw)
	}
	if len(body.Calls) != 2 {
		t.Fatalf("calls=%d body=%s", len(body.Calls), raw)
	}
	for i, call := range body.Calls {
		if call.Normalized.Usage == nil {
			t.Fatalf("call %d missing usage: %s", i, raw)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	trace := lm.Wait(ctx)
	if len(trace.Calls) != 2 {
		t.Fatalf("trace calls=%d", len(trace.Calls))
	}
}
