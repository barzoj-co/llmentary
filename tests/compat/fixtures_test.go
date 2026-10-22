// SPDX-License-Identifier: AGPL-3.0-only

package compat_test

import (
	"fmt"
	"strings"
)

const (
	chatJSONPath      = "/v1/chat/completions"
	responsesJSONPath = "/v1/responses"
)

var providers = []string{
	"ollama", "llamacpp", "deepseek", "groq", "together",
	"mistral", "gemini", "openrouter", "fixture",
}

var surfaces = []string{
	"chat.json", "chat.sse", "chat.tools", "chat.tool_result",
	"responses.json", "responses.sse",
}

func modelFor(provider string) string {
	return "compat-" + provider
}

func chatRequest(provider string, extra string) []byte {
	body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}]}`, modelFor(provider))
	if extra == "" {
		return []byte(body)
	}
	return []byte(strings.TrimSuffix(body, "}") + "," + extra + "}")
}

func chatStreamRequest(provider string) []byte {
	return []byte(fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}],"stream":true}`, modelFor(provider)))
}

func chatToolsRequest(provider string) []byte {
	return []byte(fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"get_weather","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}}]}`, modelFor(provider)))
}

func chatToolResultRequest(provider string) []byte {
	return []byte(fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Paris\"}"}}]},{"role":"tool","tool_call_id":"call_1","content":"22C"}]}`, modelFor(provider)))
}

func responsesRequest(provider string) []byte {
	return []byte(fmt.Sprintf(`{"model":%q,"input":"hi"}`, modelFor(provider)))
}

func responsesStreamRequest(provider string) []byte {
	return []byte(fmt.Sprintf(`{"model":%q,"input":"hi","stream":true}`, modelFor(provider)))
}

func chatJSONBody(provider string) []byte {
	base := `{"id":"chatcmpl-fake","object":"chat.completion","created":1,"model":%q,"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":%q%s}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}%s}`
	logprobs := `,"logprobs":null`
	finish := "stop"
	extra := ""
	switch provider {
	case "ollama", "mistral", "gemini":
		logprobs = ""
	case "llamacpp":
		logprobs = ""
		extra = `,"timings":{"prompt_ms":1}`
	case "deepseek":
		extra = `,"system_fingerprint":null`
	case "groq":
		extra = `,"service_tier":"on_demand","x_groq":{"id":"g"}`
	case "together":
		finish = "eos"
	case "openrouter":
		finish = "error"
		extra = `,"provider":"OpenRouter"`
	}
	return []byte(fmt.Sprintf(base, modelFor(provider), finish, logprobs, extra))
}

func chatSSEBody(provider string) []byte {
	finish := "null"
	extra := ""
	switch provider {
	case "together":
		finish = `"eos"`
	case "openrouter":
		finish = `"error"`
		extra = `,"provider":"OpenRouter"`
	case "groq":
		extra = `,"x_groq":{"id":"g"}`
	}
	chunk := fmt.Sprintf(`data: {"id":"c","object":"chat.completion.chunk","created":1,"model":%q,"choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":%s}]%s}`+"\n\n", modelFor(provider), finish, extra)
	return []byte(chunk + "data: [DONE]\n\n")
}

func chatToolsBody(provider string) []byte {
	return []byte(fmt.Sprintf(`{"id":"c","object":"chat.completion","created":1,"model":%q,"choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Paris\"}"}}]},"finish_reason":"tool_calls","logprobs":null}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`, modelFor(provider)))
}

func responsesJSONBody(provider string) []byte {
	return []byte(fmt.Sprintf(`{"id":"r","object":"response","created_at":1,"model":%q,"status":"completed","output":[{"id":"m","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok","annotations":[],"logprobs":[]}]}],"parallel_tool_calls":true,"tool_choice":"auto","tools":[]}`, modelFor(provider)))
}

func responsesSSEBody(provider string) []byte {
	return []byte(fmt.Sprintf("event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"r\",\"status\":\"in_progress\",\"model\":%q}}\n\nevent: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"r\",\"status\":\"completed\",\"model\":%q,\"output\":[]}}\n\n", modelFor(provider), modelFor(provider)))
}

func requestFor(provider, surface string) (path string, body []byte) {
	switch surface {
	case "chat.json":
		return chatJSONPath, chatRequest(provider, "")
	case "chat.sse":
		return chatJSONPath, chatStreamRequest(provider)
	case "chat.tools":
		return chatJSONPath, chatToolsRequest(provider)
	case "chat.tool_result":
		return chatJSONPath, chatToolResultRequest(provider)
	case "responses.json":
		return responsesJSONPath, responsesRequest(provider)
	case "responses.sse":
		return responsesJSONPath, responsesStreamRequest(provider)
	default:
		return chatJSONPath, chatRequest(provider, "")
	}
}

func responseFor(provider, surface string) (contentType string, body []byte) {
	switch surface {
	case "chat.sse":
		return "text/event-stream", chatSSEBody(provider)
	case "chat.tools", "chat.tool_result":
		return "application/json", chatToolsBody(provider)
	case "responses.json":
		return "application/json", responsesJSONBody(provider)
	case "responses.sse":
		return "text/event-stream", responsesSSEBody(provider)
	default:
		return "application/json", chatJSONBody(provider)
	}
}
