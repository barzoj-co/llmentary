//go:build e2e

package llmtestlab_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/barzoj-co/llm-test-lab/internal/lab"
	"github.com/barzoj-co/llm-test-lab/pkg/llmtestlab"
)

func TestOpenAIChatThroughProxy(t *testing.T) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		t.Skip("OPENAI_API_KEY is unset")
	}
	model := os.Getenv("OPENAI_E2E_MODEL")
	if model == "" {
		model = "gpt-4.1-mini"
	}
	cfg, err := lab.ConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Upstreams = nil
	if err := cfg.AddUpstream("openai=https://api.openai.com/v1"); err != nil {
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
	testLab := llmtestlab.New(t, llmtestlab.WithProxyURL(proxy.URL), llmtestlab.WithControlURL(control.URL), llmtestlab.WithWait(100*time.Millisecond, 5*time.Second))

	chatBody := []byte(`{
		"model":` + strconv.Quote(model) + `,
		"messages":[{"role":"user","content":"Call the echo_city tool with city Paris. Do not answer in text."}],
		"stream":true,
		"stream_options":{"include_usage":true},
		"tool_choice":{"type":"function","function":{"name":"echo_city"}},
		"tools":[{
			"type":"function",
			"function":{
				"name":"echo_city",
				"description":"Echo a city name.",
				"parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"],"additionalProperties":false}
			}
		}]
	}`)
	chatStream := postOpenAI(t, apiKey, testLab, "/chat/completions", chatBody)
	if !bytes.Contains(chatStream, []byte("data:")) {
		t.Fatalf("expected chat SSE stream, got %q", chatStream)
	}

	responsesBody := []byte(`{
		"model":` + strconv.Quote(model) + `,
		"input":"Call the echo_city tool with city Paris. Do not answer in text.",
		"stream":true,
		"store":false,
		"tool_choice":{"type":"function","name":"echo_city"},
		"tools":[{
			"type":"function",
			"name":"echo_city",
			"description":"Echo a city name.",
			"parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"],"additionalProperties":false}
		}]
	}`)
	responsesStream := postOpenAI(t, apiKey, testLab, "/responses", responsesBody)
	if !bytes.Contains(responsesStream, []byte("event:")) {
		t.Fatalf("expected responses SSE stream, got %q", responsesStream)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	assert := testLab.Expect(ctx)
	assert.CallCount(2)
	assert.ToolCalledTimes("echo_city", 2, llmtestlab.ArgsSubset(map[string]any{"city": "Paris"}))
	assert.ToolOrder("echo_city", "echo_city")
	assert.ToolNotCalled("unknown_tool")
	assert.NoErrors()
	assert.TokensBelow(2000)
}

func postOpenAI(t *testing.T, apiKey string, lab *llmtestlab.Lab, path string, body []byte) []byte {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, lab.OpenAIBaseURL("openai")+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	key, value := lab.Header()
	req.Header.Set(key, value)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("openai status=%d body=%s", resp.StatusCode, string(stream))
	}
	return stream
}
