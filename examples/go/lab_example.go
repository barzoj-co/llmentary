package main

import (
	"context"
	"net/http"
	"testing"

	"github.com/barzoj-co/llm-test-lab/pkg/llmtestlab"
)

func AgentWeatherExample(t *testing.T) {
	lab := llmtestlab.New(t)

	req, err := http.NewRequest(http.MethodPost, lab.OpenAIBaseURL("openai")+"/chat/completions", nil)
	if err != nil {
		t.Fatal(err)
	}
	key, value := lab.Header()
	req.Header.Set(key, value)
	// Send req through your app or SDK configured with lab.OpenAIBaseURL("openai").
	_ = req

	assert := lab.Expect(context.Background())
	assert.CallCount(1)
	assert.ToolCalled("get_weather", llmtestlab.ArgsSubset(map[string]any{"city": "Paris"}))
	assert.ToolNotCalled("delete_user")
	assert.TokensBelow(1000)
}

func main() {}
