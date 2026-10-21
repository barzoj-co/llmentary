// SPDX-License-Identifier: AGPL-3.0-only

//go:build e2e

package llmentary_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"
)

func TestE2EWaitMissingSessionThenRealCall(t *testing.T) {
	apiKey := requireAPIKey(t)
	p := startLiveProxy(t, "off", "relaxed")
	sid := fmt.Sprintf("ghost-live-%d", time.Now().UnixNano())
	start := time.Now()
	res, err := http.Get(p.controlURL + "/sessions/" + sid + "/wait?timeout=3s&quiet=200ms&min_calls=0")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", res.StatusCode, raw)
	}
	if time.Since(start) > 200*time.Millisecond {
		t.Fatalf("min_calls=0 took %s", time.Since(start))
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	errObj, _ := body["error"].(map[string]any)
	if fmt.Sprint(errObj["type"]) != "session_not_found" {
		t.Fatalf("error=%v", body["error"])
	}

	reqBody := []byte(`{"model":` + quoteJSON(liveModel()) + `,"input":"Reply with the single word pong.","max_output_tokens":16}`)
	status, _, resp, session := postLive(t, apiKey, p, "/responses", sid, reqBody)
	if status != 200 {
		t.Fatalf("status=%d body=%s", status, redactLiveBody(resp))
	}
	wait, err := http.Get(p.controlURL + "/sessions/" + sid + "/wait?timeout=15s&quiet=50ms&min_calls=1")
	if err != nil {
		t.Fatal(err)
	}
	wraw, err := io.ReadAll(wait.Body)
	_ = wait.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if wait.StatusCode != 200 {
		t.Fatalf("wait after call status=%d body=%s", wait.StatusCode, wraw)
	}
	_ = session
}
