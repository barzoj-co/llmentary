// SPDX-License-Identifier: AGPL-3.0-only

//go:build e2e

package llmentary_test

import (
	"context"
	"testing"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/param"
)

func TestE2EStreamNotClientAborted(t *testing.T) {
	apiKey := requireAPIKey(t)
	model := e2eModel()
	lm := newE2E(t)
	client := openai.NewClient(
		option.WithAPIKey(apiKey),
		option.WithBaseURL(lm.OpenAIBaseURL("openai")),
		option.WithHTTPClient(lm.HTTPClient()),
		option.WithMaxRetries(0),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	const repeats = 10
	for i := 0; i < repeats; i++ {
		stream := client.Chat.Completions.NewStreaming(ctx, openai.ChatCompletionNewParams{
			Model:       model,
			Temperature: param.NewOpt(0.0),
			MaxTokens:   param.NewOpt(int64(16)),
			Messages: []openai.ChatCompletionMessageParamUnion{
				openai.UserMessage("Reply with the single word pong."),
			},
			StreamOptions: openai.ChatCompletionStreamOptionsParam{
				IncludeUsage: openai.Bool(true),
			},
		})
		var sawChunk bool
		for stream.Next() {
			sawChunk = true
		}
		if err := stream.Err(); err != nil {
			t.Fatalf("stream %d: %v", i, err)
		}
		_ = stream.Close()
		if !sawChunk {
			t.Fatalf("stream %d: no chunks", i)
		}
	}
	trace := lm.Wait(ctx)
	chat := trace.Query().APIType("chat").Calls()
	assertCallCount(t, chat, repeats)
	for i, call := range chat {
		if call.Normalized.ClientAborted {
			t.Fatalf("call %d client_aborted=true finish=%s warnings=%v", i, call.Normalized.FinishReason, call.Normalized.ParserWarnings)
		}
		if call.HTTPStatus != 200 {
			t.Fatalf("call %d http=%d", i, call.HTTPStatus)
		}
	}
}
