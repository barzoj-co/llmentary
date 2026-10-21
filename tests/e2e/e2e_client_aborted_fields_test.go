// SPDX-License-Identifier: AGPL-3.0-only

//go:build e2e

package llmentary_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/responses"
)

func TestE2EClientAbortedTypedFields(t *testing.T) {
	apiKey := requireAPIKey(t)
	p := startLiveProxy(t, "off", "relaxed")
	sid := liveSession()
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
	call := liveLastCall(t, waitLiveControl(t, p.controlURL, sid))
	n := liveNormalized(call)
	if call["client_aborted"] != true || n["client_aborted"] != true {
		t.Fatalf("client_aborted top=%v nested=%v", call["client_aborted"], n["client_aborted"])
	}
	if phase := call["abort_phase"]; phase != "before_headers" && phase != "after_headers" {
		t.Fatalf("abort_phase=%v", phase)
	}
	if n["finish_reason"] != "cancelled" {
		t.Fatalf("finish_reason=%v", n["finish_reason"])
	}
	if call["http_status"] == float64(http.StatusBadGateway) {
		t.Fatal("http_status=502")
	}
}
