// SPDX-License-Identifier: AGPL-3.0-only

//go:build e2e

package llmtestlab_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/barzoj-co/llm-test-lab/internal/lab"
	"github.com/barzoj-co/llm-test-lab/internal/tracetest"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/param"
)

func TestE2EChatCompletionsToolFlow(t *testing.T) {
	apiKey := requireAPIKey(t)
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

	trace := testLab.Wait(ctx)
	chat := trace.Query().APIType("chat").Calls()
	assertCallCount(t, chat, 2)
	assertToolCalledSubset(t, chat, "echo_city", map[string]any{"city": "Paris"})
	assertToolResultFedBack(t, chat, toolCall.ID)
	assertFinishReason(t, chat, "stop")
	assertResponseContains(t, chat, "Paris")
	assertNoErrors(t, chat)
	assertTokensAtMost(t, chat, 2000)
	assertTTFTBelow(t, chat, 10*time.Second)
	assertLatencyBelow(t, chat, 20*time.Second)
	assertNoRetries(t, chat)
	assertNoParserWarnings(t, trace.Calls)
}

func TestE2EResponsesToolFlow(t *testing.T) {
	apiKey := requireAPIKey(t)
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
	trace := testLab.Wait(finalCtx)
	responses := trace.Query().APIType("responses").Calls()
	assertCallCount(t, responses, 2)
	assertToolCalledSubset(t, responses, "echo_city", map[string]any{"city": "Paris"})
	assertToolResultFedBack(t, responses, toolCallID)
	assertFinishReason(t, responses, "stop")
	assertResponseContains(t, responses, "Paris")
	assertNoErrors(t, responses)
	assertTokensAtMost(t, responses, 2000)
	assertTTFTBelow(t, responses, 10*time.Second)
	assertLatencyBelow(t, responses, 20*time.Second)
	assertNoRetries(t, responses)
	assertNoParserWarnings(t, trace.Calls)
}

func TestE2EChatCompletionsNonStreaming(t *testing.T) {
	apiKey := requireAPIKey(t)
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
	trace := testLab.Wait(finalCtx)
	assertCallCount(t, trace.Calls, 2)
	chat := trace.Query().APIType("chat").Calls()
	assertToolCalledSubset(t, chat, "echo_city", map[string]any{"city": "Paris"})
	assertToolResultFedBack(t, chat, toolCall.ID)
	assertFinishReason(t, chat, "stop")
	assertResponseContains(t, chat, "Paris")
	assertNoErrors(t, trace.Calls)
	assertNoParserWarnings(t, trace.Calls)
}

func postOpenAI(t *testing.T, apiKey string, lab *tracetest.Lab, path string, body []byte) []byte {
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

func newE2ELab(t *testing.T) *tracetest.Lab {
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
	return tracetest.New(t, tracetest.WithProxyURL(proxy.URL), tracetest.WithControlURL(control.URL), tracetest.WithWait(100*time.Millisecond, 5*time.Second))
}

func e2eModel() string {
	if model := os.Getenv("OPENAI_E2E_MODEL"); model != "" {
		return model
	}
	return "gpt-4.1-mini"
}

func TestE2EChatNonStreamAndStreamText(t *testing.T) {
	apiKey := requireAPIKey(t)
	model := e2eModel()
	testLab := newE2ELab(t)
	nonStream := []byte(`{"model":` + strconv.Quote(model) + `,"messages":[{"role":"user","content":"Reply with the single word pong."}],"temperature":0,"max_tokens":16}`)
	body := postOpenAI(t, apiKey, testLab, "/chat/completions", nonStream)
	if !bytes.Contains(bytes.ToLower(body), []byte("pong")) {
		t.Fatalf("expected pong in chat body, got %s", body)
	}
	stream := []byte(`{"model":` + strconv.Quote(model) + `,"stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"Reply with the single word pong."}],"temperature":0,"max_tokens":16}`)
	streamBody := postOpenAI(t, apiKey, testLab, "/chat/completions", stream)
	if !bytes.Contains(streamBody, []byte("data:")) {
		t.Fatalf("expected chat SSE, got %s", streamBody)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	trace := testLab.Wait(ctx)
	chat := trace.Query().APIType("chat").Calls()
	assertCallCount(t, chat, 2)
	assertResponseContains(t, chat, "pong")
	assertNoErrors(t, chat)
	for _, call := range chat {
		if call.Normalized.Usage == nil || call.Normalized.Usage.TotalTokens == nil {
			t.Fatalf("expected usage on chat call #%d: %+v", call.Seq, call.Normalized.Usage)
		}
	}
	assertNoParserWarnings(t, trace.Calls)
}

func TestE2EChatParallelToolCalls(t *testing.T) {
	apiKey := requireAPIKey(t)
	model := e2eModel()
	testLab := newE2ELab(t)
	body := []byte(`{
		"model":` + strconv.Quote(model) + `,
		"messages":[{"role":"user","content":"You must call BOTH tools in this turn: echo_city with city Paris AND echo_country with country France. Do not answer in text."}],
		"temperature":0,
		"parallel_tool_calls":true,
		"tool_choice":"required",
		"tools":[` + echoCityChatToolJSON() + `,` + echoCountryChatToolJSON() + `]
	}`)
	_ = postOpenAIRetryNetwork(t, apiKey, testLab, "/chat/completions", body)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	trace := testLab.Wait(ctx)
	chat := trace.Query().APIType("chat").Calls()
	assertCallCount(t, chat, 1)
	if n := len(chat[0].Normalized.ToolCalls); n < 2 {
		t.Fatalf("expected two parallel tool calls (echo_city + echo_country), got %d:\n%s", n, trace.Query().APIType("chat").Trace().Summary())
	}
	assertToolCalled(t, chat, "echo_city")
	assertToolCalled(t, chat, "echo_country")
	assertNoParserWarnings(t, trace.Calls)
}

func TestE2EChatStructuredOutput(t *testing.T) {
	apiKey := requireAPIKey(t)
	model := e2eModel()
	testLab := newE2ELab(t)
	body := []byte(`{
		"model":` + strconv.Quote(model) + `,
		"messages":[{"role":"user","content":"Return JSON with answer=ok and count=1."}],
		"temperature":0,
		"response_format":{"type":"json_schema","json_schema":{"name":"ans","strict":true,"schema":{"type":"object","properties":{"answer":{"type":"string"},"count":{"type":"integer"}},"required":["answer","count"],"additionalProperties":false}}}
	}`)
	_ = postOpenAI(t, apiKey, testLab, "/chat/completions", body)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	trace := testLab.Wait(ctx)
	chat := trace.Query().APIType("chat").Calls()
	assertCallCount(t, chat, 1)
	assertResponseJSONSchemaValid(t, chat, map[string]any{
		"type":     "object",
		"required": []string{"answer", "count"},
		"properties": map[string]any{
			"answer": map[string]any{"type": "string"},
			"count":  map[string]any{"type": "integer"},
		},
	})
	assertNoErrors(t, chat)
	assertNoParserWarnings(t, trace.Calls)
}

func TestE2EResponsesNonStreamAndStructured(t *testing.T) {
	apiKey := requireAPIKey(t)
	model := e2eModel()
	testLab := newE2ELab(t)
	text := []byte(`{"model":` + strconv.Quote(model) + `,"input":"Reply with the single word pong.","temperature":0,"max_output_tokens":16}`)
	body := postOpenAI(t, apiKey, testLab, "/responses", text)
	if !bytes.Contains(bytes.ToLower(body), []byte("pong")) {
		t.Fatalf("expected pong in responses body, got %s", body)
	}
	structured := []byte(`{
		"model":` + strconv.Quote(model) + `,
		"input":"Return JSON with answer=ok.",
		"temperature":0,
		"text":{"format":{"type":"json_schema","name":"ans","strict":true,"schema":{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"],"additionalProperties":false}}}
	}`)
	_ = postOpenAI(t, apiKey, testLab, "/responses", structured)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	trace := testLab.Wait(ctx)
	responses := trace.Query().APIType("responses").Calls()
	assertCallCount(t, responses, 2)
	assertResponseContains(t, responses, "pong")
	assertResponseJSONSchemaValid(t, responses, map[string]any{
		"type":     "object",
		"required": []string{"answer"},
		"properties": map[string]any{
			"answer": map[string]any{"type": "string"},
		},
	})
	assertNoErrors(t, responses)
	for _, call := range responses {
		if call.Normalized.Usage == nil || call.Normalized.Usage.TotalTokens == nil {
			t.Fatalf("expected usage on responses call #%d: %+v", call.Seq, call.Normalized.Usage)
		}
	}
	assertNoParserWarnings(t, trace.Calls)
}

func TestE2EInvalidParameter400(t *testing.T) {
	apiKey := requireAPIKey(t)
	model := e2eModel()
	testLab := newE2ELab(t)
	body := []byte(`{"model":` + strconv.Quote(model) + `,"messages":[{"role":"user","content":"hi"}],"temperature":99}`)
	status, _, resp := postOpenAIStatus(t, apiKey, testLab, "/chat/completions", body)
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", status, resp)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	trace := testLab.Wait(ctx)
	assertHasError(t, trace.Calls)
	call := trace.Calls[0]
	if call.Normalized.Error == nil || call.Normalized.Error.Normalized != "bad_request" || call.HTTPStatus != 400 {
		t.Fatalf("error=%+v status=%d", call.Normalized.Error, call.HTTPStatus)
	}
	t.Logf("live OpenAI 400 body shape: %s", resp)
	assertNoParserWarnings(t, trace.Calls)
}

func TestE2EUnauthorized401(t *testing.T) {
	_ = requireAPIKey(t)
	model := e2eModel()
	testLab := newE2ELab(t)
	body := []byte(`{"model":` + strconv.Quote(model) + `,"messages":[{"role":"user","content":"hi"}]}`)
	status, _, resp := postOpenAIStatus(t, "sk-invalid-e2e-key", testLab, "/chat/completions", body)
	if status != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", status, resp)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	trace := testLab.Wait(ctx)
	assertHasError(t, trace.Calls)
	call := trace.Calls[0]
	if call.Normalized.Error == nil || call.Normalized.Error.Normalized != "auth" || call.HTTPStatus != 401 {
		t.Fatalf("error=%+v status=%d", call.Normalized.Error, call.HTTPStatus)
	}
	t.Logf("live OpenAI 401 body shape: %s", resp)
	assertNoParserWarnings(t, trace.Calls)
}

func TestE2EResponsesStreamErrorShape(t *testing.T) {
	apiKey := requireAPIKey(t)
	model := e2eModel()
	testLab := newE2ELab(t)
	// A real mid-stream Responses `event: error` is not cheaply triggerable;
	// fixture coverage remains D4. This live probe pins the pre-stream JSON
	// error for previous_response_id:"resp_does_not_exist".
	body := []byte(`{"model":` + strconv.Quote(model) + `,"stream":true,"previous_response_id":"resp_does_not_exist","input":"hi"}`)
	status, hdr, resp := postOpenAIStatus(t, apiKey, testLab, "/responses", body)
	row := loadLiveBehavior(t).Rows["stream_error_responses"]
	if status != row.Status {
		t.Fatalf("status=%d want %d body=%s", status, row.Status, resp)
	}
	ct := hdr.Get("Content-Type")
	if row.ContentType != "" && !strings.Contains(strings.ToLower(ct), strings.ToLower(row.ContentType)) {
		t.Fatalf("Content-Type=%q want %q body=%s", ct, row.ContentType, resp)
	}
	if strings.Contains(strings.ToLower(ct), "event-stream") {
		t.Fatalf("Content-Type must be application/json, not text/event-stream; got %q body=%s", ct, resp)
	}
	if bytes.Contains(resp, []byte("event:")) || bytes.HasPrefix(bytes.TrimSpace(resp), []byte("data:")) {
		t.Fatalf("Content-Type must be application/json, not text/event-stream; body=%s", resp)
	}
	var env map[string]any
	if err := json.Unmarshal(resp, &env); err != nil {
		t.Fatalf("expected application/json error, not SSE: %v body=%s", err, resp)
	}
	errObj, _ := env["error"].(map[string]any)
	if want, _ := row.Error["type"].(string); want != "" && fmt.Sprint(errObj["type"]) != want {
		t.Fatalf("error.type=%v want %s", errObj["type"], want)
	}
	if want, ok := row.Error["code"]; ok && !liveCodeEqual(errObj["code"], want) {
		t.Fatalf("error.code=%v want %v", errObj["code"], want)
	}
	if want, ok := row.Error["param"].(string); ok && want != "" && fmt.Sprint(errObj["param"]) != want {
		t.Fatalf("error.param=%v want %s", errObj["param"], want)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	trace := testLab.Wait(ctx)
	if len(trace.Calls) == 0 {
		t.Fatal("expected a recorded call")
	}
	call := trace.Calls[0]
	if call.Normalized.Error == nil {
		t.Fatal("normalized.error missing")
	}
	if call.Normalized.Error.MidStream {
		t.Fatal("normalized.error.mid_stream must be false for a pre-stream JSON error")
	}
	if call.Normalized.Error.HTTPStatus != status {
		t.Fatalf("normalized.error.http_status=%d want %d", call.Normalized.Error.HTTPStatus, status)
	}
}

func requireAPIKey(t *testing.T) string {
	t.Helper()
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		t.Fatal("OPENAI_API_KEY is required; e2e tests must not be skipped")
	}
	return apiKey
}

func postOpenAIStatus(t *testing.T, apiKey string, lab *tracetest.Lab, path string, body []byte) (int, http.Header, []byte) {
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
	raw, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, resp.Header.Clone(), raw
}

func assertNoParserWarnings(t *testing.T, calls []lab.Call) {
	t.Helper()
	for _, call := range calls {
		if len(call.Normalized.ParserWarnings) != 0 {
			t.Fatalf("parser_warnings must be empty for live OpenAI call #%d: %v", call.Seq, call.Normalized.ParserWarnings)
		}
	}
}

func postOpenAIRetryNetwork(t *testing.T, apiKey string, lab *tracetest.Lab, path string, body []byte) []byte {
	t.Helper()
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
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
			lastErr = err
			t.Logf("network error on attempt %d/3: %v", attempt, err)
			time.Sleep(time.Duration(attempt) * 400 * time.Millisecond)
			continue
		}
		raw, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			t.Fatalf("read error after response (call already recorded): %v", readErr)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("openai status=%d body=%s", resp.StatusCode, string(raw))
		}
		return raw
	}
	t.Fatalf("network retries exhausted: %v", lastErr)
	return nil
}

func echoCountryChatToolJSON() string {
	return `{
		"type":"function",
		"function":{
			"name":"echo_country",
			"description":"Echo a country name.",
			"parameters":{"type":"object","properties":{"country":{"type":"string"}},"required":["country"],"additionalProperties":false}
		}
	}`
}

func echoCityChatToolJSON() string {
	return `{
		"type":"function",
		"function":{
			"name":"echo_city",
			"description":"Echo a city name.",
			"parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"],"additionalProperties":false}
		}
	}`
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
