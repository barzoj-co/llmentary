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
	client, err := llmtestlab.NewClient(control.URL)
	if err != nil {
		t.Fatal(err)
	}
	sessionID := client.NewSession()

	body := []byte(`{
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
	req, err := http.NewRequest(http.MethodPost, proxy.URL+"/u/openai/chat/completions", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(llmtestlab.SessionHeaderName, sessionID)
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
	if !bytes.Contains(stream, []byte("data:")) {
		t.Fatalf("expected SSE stream, got %q", stream)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	calls, err := client.Wait(ctx, sessionID, 100*time.Millisecond, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	llmtestlab.AssertCallCount(t, calls, 1)
	llmtestlab.AssertToolCalled(t, calls, "echo_city", map[string]any{"city": "Paris"})
	llmtestlab.AssertToolOrder(t, calls, "echo_city")
	llmtestlab.AssertToolNotCalled(t, calls, "unknown_tool")
	llmtestlab.AssertTokensBelow(t, calls, 1000)
}
