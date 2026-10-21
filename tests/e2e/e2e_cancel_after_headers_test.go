// SPDX-License-Identifier: AGPL-3.0-only

//go:build e2e

package llmentary_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/responses"
)

func TestE2ECancelAfterHeadersChat(t *testing.T) {
	apiKey := requireAPIKey(t)
	model := liveModel()
	p := startLiveProxy(t, "off", "relaxed")
	sid := liveSession()
	client := openai.NewClient(
		option.WithAPIKey(apiKey),
		option.WithBaseURL(p.proxyURL+"/s/"+sid+"/v1"),
		option.WithMaxRetries(0),
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := client.Chat.Completions.NewStreaming(ctx, openai.ChatCompletionNewParams{
		Model: model,
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.UserMessage("Write a 600-word bedtime story with many chapters."),
		},
		StreamOptions: openai.ChatCompletionStreamOptionsParam{
			IncludeUsage: openai.Bool(true),
		},
	})
	var saw bool
	for stream.Next() {
		ev := stream.Current()
		if len(ev.Choices) > 0 && ev.Choices[0].Delta.Content != "" {
			saw = true
			cancel()
			break
		}
	}
	_ = stream.Err()
	_ = stream.Close()
	if !saw {
		t.Fatal("no chat content chunk before cancel")
	}
	assertLiveAfterHeadersAbort(t, waitLiveControl(t, p.controlURL, sid))
}

func TestE2ECancelAfterHeadersResponses(t *testing.T) {
	apiKey := requireAPIKey(t)
	model := liveModel()
	p := startLiveProxy(t, "off", "relaxed")
	sid := liveSession()
	client := openai.NewClient(
		option.WithAPIKey(apiKey),
		option.WithBaseURL(p.proxyURL+"/s/"+sid+"/v1"),
		option.WithMaxRetries(0),
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := client.Responses.NewStreaming(ctx, responses.ResponseNewParams{
		Model: model,
		Input: responses.ResponseNewParamsInputUnion{
			OfString: openai.String("Write a 600-word bedtime story with many chapters."),
		},
		MaxOutputTokens: openai.Int(800),
	})
	var saw bool
	for stream.Next() {
		ev := stream.Current()
		if ev.Type == "response.output_text.delta" || strings.Contains(ev.Type, "output_text.delta") {
			saw = true
			cancel()
			break
		}
	}
	_ = stream.Err()
	_ = stream.Close()
	if !saw {
		t.Fatal("no responses content delta before cancel")
	}
	assertLiveAfterHeadersAbort(t, waitLiveControl(t, p.controlURL, sid))
}

func assertLiveAfterHeadersAbort(t *testing.T, session map[string]any) {
	t.Helper()
	call := liveLastCall(t, session)
	n := liveNormalized(call)
	if n["client_aborted"] != true {
		t.Fatalf("client_aborted=%v http=%v finish=%v warnings=%v", n["client_aborted"], call["http_status"], n["finish_reason"], n["parser_warnings"])
	}
	if call["http_status"] != float64(http.StatusOK) {
		t.Fatalf("http_status=%v want 200", call["http_status"])
	}
	if n["finish_reason"] != "cancelled" {
		t.Fatalf("finish_reason=%v", n["finish_reason"])
	}
	if call["upstream_called"] != true {
		t.Fatalf("upstream_called=%v", call["upstream_called"])
	}
	errObj, _ := n["error"].(map[string]any)
	if liveString(errObj["normalized"]) == "network" {
		t.Fatalf("network error: %v", n["error"])
	}
	if errObj["mid_stream"] == true {
		t.Fatalf("mid_stream set: %v", n["error"])
	}
	if liveString(n["output_text"]) == "" {
		t.Fatalf("expected partial output_text, got %q", n["output_text"])
	}
}

func liveString(v any) string {
	s, _ := v.(string)
	return s
}
