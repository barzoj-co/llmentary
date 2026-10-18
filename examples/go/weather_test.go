// SPDX-License-Identifier: AGPL-3.0-only

package example

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/barzoj-co/llm-test-lab/internal/lab"
	"github.com/barzoj-co/llm-test-lab/pkg/llmtestlab"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

func TestWeatherTool(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"chatcmpl_example",
			"model":"gpt-fixture",
			"choices":[{
				"message":{"role":"assistant","tool_calls":[{
					"id":"call_weather",
					"type":"function",
					"function":{"name":"get_weather","arguments":"{\"city\":\"Paris\"}"}
				}]},
				"finish_reason":"tool_calls"
			}],
			"usage":{"prompt_tokens":8,"completion_tokens":4,"total_tokens":12}
		}`))
	}))
	defer upstream.Close()

	cfg := lab.Config{ListenAddr: ":0", ControlAddr: "127.0.0.1:0"}
	if err := cfg.AddUpstream("openai=" + upstream.URL); err != nil {
		t.Fatal(err)
	}
	server, err := lab.NewServer(cfg, lab.NewMemoryStore(), lab.TolerantParser{})
	if err != nil {
		t.Fatal(err)
	}
	proxy := httptest.NewServer(server.ProxyHandler())
	defer proxy.Close()
	control := httptest.NewServer(server.ControlHandler())
	defer control.Close()

	testLab := llmtestlab.New(t,
		llmtestlab.WithProxyURL(proxy.URL),
		llmtestlab.WithControlURL(control.URL),
		llmtestlab.WithWait(time.Millisecond, time.Second),
	)
	client := openai.NewClient(
		option.WithAPIKey("test-key"),
		option.WithBaseURL(testLab.OpenAIBaseURL("openai")),
		option.WithHTTPClient(testLab.HTTPClient()),
		option.WithMaxRetries(0),
	)

	_, err = client.Chat.Completions.New(context.Background(), openai.ChatCompletionNewParams{
		Model: "gpt-fixture",
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.UserMessage("Weather in Paris?"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	assert := testLab.Expect(context.Background())
	assert.ToolCalled("get_weather", llmtestlab.ArgsSubset(map[string]any{"city": "Paris"}))
	assert.FinishReason("tool_calls")
	assert.TokensAtMost(20)
	assert.NoErrors()
}
