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

func TestE2EClientCancelBeforeHeaders(t *testing.T) {
	apiKey := requireAPIKey(t)
	model := liveModel()
	p := startLiveProxy(t, "off", "relaxed")
	const repeats = 5
	for i := 0; i < repeats; i++ {
		sid := liveSession()
		client := openai.NewClient(
			option.WithAPIKey(apiKey),
			option.WithBaseURL(p.proxyURL+"/s/"+sid+"/v1"),
			option.WithMaxRetries(0),
		)
		ctx, cancel := context.WithCancel(context.Background())
		time.AfterFunc(50*time.Millisecond, cancel)
		stream := client.Responses.NewStreaming(ctx, responses.ResponseNewParams{
			Model: model,
			Input: responses.ResponseNewParamsInputUnion{
				OfString: openai.String("Write a very long bedtime story with many chapters, at least two thousand words."),
			},
		})
		for stream.Next() {
		}
		_ = stream.Err()
		_ = stream.Close()

		session := waitLiveControl(t, p.controlURL, sid)
		call := liveLastCall(t, session)
		n := liveNormalized(call)
		if n["client_aborted"] != true {
			t.Fatalf("call %d client_aborted=%v http=%v finish=%v warnings=%v", i, n["client_aborted"], call["http_status"], n["finish_reason"], n["parser_warnings"])
		}
		if call["http_status"] == float64(http.StatusBadGateway) {
			t.Fatalf("call %d http_status=502", i)
		}
	}
}

func waitLiveControl(t *testing.T, controlURL, session string) map[string]any {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, controlURL+"/sessions/"+session+"/wait?quiet=50ms&timeout=15s&min_calls=1", nil)
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
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("session json: %v body=%s", err, raw)
	}
	return out
}
