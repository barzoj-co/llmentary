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
	"strings"
	"testing"
	"time"

	"github.com/barzoj-co/llm-test-lab/internal/lab"
	"github.com/barzoj-co/llm-test-lab/pkg/llmtestlab"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/param"
)

func TestE2EChatCompletionsToolFlow(t *testing.T) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		t.Skip("OPENAI_API_KEY is unset")
	}
	model := e2eModel()
	testLab := newE2ELab(t)
	client := openai.NewClient(
		option.WithAPIKey(apiKey),
		option.WithBaseURL(testLab.OpenAIBaseURL("openai")),
		option.WithHTTPClient(testLab.HTTPClient()),
		option.WithMaxRetries(0),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	params := openai.ChatCompletionNewParams{
		Model:       model,
		Temperature: param.NewOpt(0.0),
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.UserMessage("Call the echo_city tool with city Paris. Do not answer in text."),
		},
		Tools: []openai.ChatCompletionToolParam{echoCityChatTool()},
		ToolChoice: openai.ChatCompletionToolChoiceOptionParamOfChatCompletionNamedToolChoice(openai.ChatCompletionNamedToolChoiceFunctionParam{
			Name: "echo_city",
		}),
	}
	first, err := client.Chat.Completions.New(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Choices) == 0 || len(first.Choices[0].Message.ToolCalls) == 0 {
		t.Fatalf("expected chat tool call, got %#v", first)
	}
	toolCall := first.Choices[0].Message.ToolCalls[0]
	params.Messages = append(params.Messages, first.Choices[0].Message.ToParam())
	params.Messages = append(params.Messages, openai.ToolMessage(`{"city":"Paris","echoed":true}`, toolCall.ID))
	params.ToolChoice = openai.ChatCompletionToolChoiceOptionUnionParam{}
	final, err := client.Chat.Completions.New(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	if len(final.Choices) == 0 || !strings.Contains(final.Choices[0].Message.Content, "Paris") {
		t.Fatalf("expected final chat answer to mention Paris, got %#v", final)
	}

	assert := testLab.Expect(ctx)
	chat := assert.Trace().Query().APIType("chat").Expect(t)
	chat.CallCount(2)
	chat.ToolCalled("echo_city", llmtestlab.ArgsSubset(map[string]any{"city": "Paris"}))
	chat.ToolResultFedBack(toolCall.ID)
	chat.FinishReason("stop")
	chat.ResponseContains("Paris")
	chat.NoErrors()
	chat.TokensAtMost(2000)
	chat.TTFTBelow(10 * time.Second)
	chat.LatencyBelow(20 * time.Second)
	chat.NoRetries()
}

func TestE2EResponsesToolFlow(t *testing.T) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		t.Skip("OPENAI_API_KEY is unset")
	}
	model := e2eModel()
	testLab := newE2ELab(t)

	firstBody := []byte(`{
		"model":` + strconv.Quote(model) + `,
		"input":"Call the echo_city tool with city Paris. Do not answer in text.",
		"stream":true,
		"temperature":0,
		"tool_choice":{"type":"function","name":"echo_city"},
		"tools":[{
			"type":"function",
			"name":"echo_city",
			"description":"Echo a city name.",
			"parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"],"additionalProperties":false}
		}]
	}`)
	responsesStream := postOpenAI(t, apiKey, testLab, "/responses", firstBody)
	if !bytes.Contains(responsesStream, []byte("event:")) {
		t.Fatalf("expected responses SSE stream, got %q", responsesStream)
	}
	firstCtx, firstCancel := context.WithTimeout(context.Background(), 10*time.Second)
	firstTrace := testLab.Wait(firstCtx)
	firstCancel()
	responseCalls := firstTrace.Query().APIType("responses").Calls()
	if len(responseCalls) != 1 || len(responseCalls[0].Normalized.ToolCalls) != 1 {
		t.Fatalf("expected one responses tool call:\n%s", firstTrace.Summary())
	}
	toolCallID := responseCalls[0].Normalized.ToolCalls[0].ID
	responseID := responseCalls[0].RequestIDs["body.id"]
	if responseID == "" {
		t.Fatalf("first Responses call did not record body.id:\n%s", firstTrace.Summary())
	}

	secondBody := []byte(`{
		"model":` + strconv.Quote(model) + `,
		"stream":true,
		"previous_response_id":` + strconv.Quote(responseID) + `,
		"input":[
			{"type":"function_call_output","call_id":` + strconv.Quote(toolCallID) + `,"output":"{\"city\":\"Paris\",\"echoed\":true}"},
			{"role":"user","content":"Now give the final answer in text and include Paris."}
		],
		"tools":[{
			"type":"function",
			"name":"echo_city",
			"description":"Echo a city name.",
			"parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"],"additionalProperties":false}
		}]
	}`)
	finalStream := postOpenAI(t, apiKey, testLab, "/responses", secondBody)
	if !bytes.Contains(finalStream, []byte("Paris")) {
		t.Fatalf("expected final responses stream to mention Paris, got %q", finalStream)
	}

	finalCtx, finalCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer finalCancel()
	assert := testLab.Expect(finalCtx)
	responses := assert.Trace().Query().APIType("responses").Expect(t)
	responses.CallCount(2)
	responses.ToolCalled("echo_city", llmtestlab.ArgsSubset(map[string]any{"city": "Paris"}))
	responses.ToolResultFedBack(toolCallID)
	responses.FinishReason("stop")
	responses.ResponseContains("Paris")
	responses.NoErrors()
	responses.TokensAtMost(2000)
	responses.TTFTBelow(10 * time.Second)
	responses.LatencyBelow(20 * time.Second)
	responses.NoRetries()
}

func TestE2EChatCompletionsNonStreaming(t *testing.T) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		t.Skip("OPENAI_API_KEY is unset")
	}
	model := e2eModel()
	testLab := newE2ELab(t)
	body := []byte(`{
		"model":` + strconv.Quote(model) + `,
		"messages":[{"role":"user","content":"Call the echo_city tool with city Paris. Do not answer in text."}],
		"stream":false,
		"temperature":0,
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
	_ = postOpenAI(t, apiKey, testLab, "/chat/completions", body)
	firstCtx, firstCancel := context.WithTimeout(context.Background(), 5*time.Second)
	firstTrace := testLab.Wait(firstCtx)
	firstCancel()
	chatCalls := firstTrace.Query().APIType("chat").Calls()
	if len(chatCalls) != 1 || len(chatCalls[0].Normalized.ToolCalls) != 1 {
		t.Fatalf("expected one chat tool call:\n%s", firstTrace.Summary())
	}
	toolCall := chatCalls[0].Normalized.ToolCalls[0]
	finalBody := []byte(`{
		"model":` + strconv.Quote(model) + `,
		"messages":[
			{"role":"user","content":"Call the echo_city tool with city Paris. Do not answer in text."},
			{"role":"assistant","tool_calls":[{"id":` + strconv.Quote(toolCall.ID) + `,"type":"function","function":{"name":"echo_city","arguments":` + strconv.Quote(toolCall.ArgumentsRaw) + `}}]},
			{"role":"tool","tool_call_id":` + strconv.Quote(toolCall.ID) + `,"content":"{\"city\":\"Paris\",\"echoed\":true}"},
			{"role":"user","content":"Now give the final answer in text and include Paris."}
		],
		"temperature":0
	}`)
	_ = postOpenAI(t, apiKey, testLab, "/chat/completions", finalBody)
	finalCtx, finalCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer finalCancel()
	assert := testLab.Expect(finalCtx)
	assert.CallCount(2)
	chat := assert.Trace().Query().APIType("chat").Expect(t)
	chat.ToolCalled("echo_city", llmtestlab.ArgsSubset(map[string]any{"city": "Paris"}))
	chat.ToolResultFedBack(toolCall.ID)
	chat.FinishReason("stop")
	chat.ResponseContains("Paris")
	assert.NoErrors()
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

func newE2ELab(t *testing.T) *llmtestlab.Lab {
	t.Helper()
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
	t.Cleanup(proxy.Close)
	control := httptest.NewServer(server.ControlHandler())
	t.Cleanup(control.Close)
	return llmtestlab.New(t, llmtestlab.WithProxyURL(proxy.URL), llmtestlab.WithControlURL(control.URL), llmtestlab.WithWait(100*time.Millisecond, 5*time.Second))
}

func e2eModel() string {
	if model := os.Getenv("OPENAI_E2E_MODEL"); model != "" {
		return model
	}
	return "gpt-4.1-mini"
}

func echoCityChatTool() openai.ChatCompletionToolParam {
	return openai.ChatCompletionToolParam{
		Function: openai.FunctionDefinitionParam{
			Name:        "echo_city",
			Description: param.NewOpt("Echo a city name."),
			Parameters: openai.FunctionParameters{
				"type": "object",
				"properties": map[string]any{
					"city": map[string]any{"type": "string"},
				},
				"required":             []string{"city"},
				"additionalProperties": false,
			},
		},
	}
}
