// SPDX-License-Identifier: AGPL-3.0-only

//go:build e2e

package llmentary_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/responses"
)

func TestE2ESessionsSpecCounters(t *testing.T) {
	apiKey := requireAPIKey(t)
	p := startLiveProxy(t, "warn", "relaxed")
	sid := liveSession()
	body := []byte(`{"model":` + quoteJSON(liveModel()) + `,"input":"Reply with the single word pong.","max_output_tokens":16}`)
	status, _, resp, _ := postLive(t, apiKey, p, "/responses", sid, body)
	if status != 200 {
		t.Fatalf("clean status=%d body=%s", status, redactLiveBody(resp))
	}

	client := openai.NewClient(
		option.WithAPIKey(apiKey),
		option.WithBaseURL(p.proxyURL+"/s/"+sid+"/v1"),
		option.WithMaxRetries(0),
	)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	stream := client.Responses.NewStreaming(ctx, responses.ResponseNewParams{
		Model: liveModel(),
		Input: responses.ResponseNewParamsInputUnion{
			OfString: openai.String("Write a very long bedtime story with many chapters, at least two thousand words."),
		},
	})
	for stream.Next() {
	}
	_ = stream.Err()
	_ = stream.Close()
	_ = waitLiveControl(t, p.controlURL, sid)

	req, err := http.NewRequest(http.MethodGet, p.controlURL+"/sessions", nil)
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
	if res.StatusCode != 200 {
		t.Fatalf("GET /sessions=%d body=%s", res.StatusCode, raw)
	}
	var out struct {
		Sessions []map[string]any `json:"sessions"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	var spec map[string]any
	for _, s := range out.Sessions {
		if fmtStringE2E(s["id"]) == sid {
			spec, _ = s["spec"].(map[string]any)
			break
		}
	}
	if spec == nil {
		t.Fatalf("session %s missing spec: %s", sid, raw)
	}
	if asInt(spec["clean"]) != 1 || asInt(spec["request_only"]) != 1 {
		t.Fatalf("spec=%v want clean=1 request_only=1", spec)
	}
	for _, pair := range [][2]string{{"checked", "Checked"}, {"clean", "Clean"}, {"findings", "Findings"}, {"request_only", "RequestOnly"}, {"not_checked", "NotChecked"}} {
		if spec[pair[0]] != spec[pair[1]] {
			t.Fatalf("alias mismatch %s=%v %s=%v", pair[0], spec[pair[0]], pair[1], spec[pair[1]])
		}
	}
}

func fmtStringE2E(v any) string {
	s, _ := v.(string)
	return s
}

func asInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	default:
		return -1
	}
}
