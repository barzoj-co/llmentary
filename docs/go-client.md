# Go testing client

The Go package `github.com/barzoj-co/llm-test-lab/pkg/llmtestlab` provides the test-facing
API for deterministic LLM integration tests. It never uses an LLM judge. Assertions run
against calls recorded by the local proxy.

## Session per test

```go
func TestAgent(t *testing.T) {
    lab := llmtestlab.New(t,
        llmtestlab.WithProxyURL("http://127.0.0.1:7777"),
        llmtestlab.WithControlURL("http://127.0.0.1:7778"),
    )

    baseURL := lab.OpenAIBaseURL("openai") // http://127.0.0.1:7777/u/openai
    key, value := lab.Header()             // X-Test-Session: <unique id>
    baggage := lab.Baggage()               // llmtestlab.session.id=<id>
    httpClient := lab.HTTPClient()         // injects X-Test-Session

    _, _, _ = baseURL, key, value
    _, _ = baggage, httpClient

    assert := lab.Expect(context.Background()) // waits for quiet session and fails fatally
    assert.CallCount(1)
}
```

`New(t)` registers `t.Cleanup` to delete the session from the proxy. It reads defaults from:

- `LLMTESTLAB_PROXY_URL` (default `http://127.0.0.1:7777`)
- `LLMTESTLAB_CONTROL_URL` (default `http://127.0.0.1:7778`)
- `LLMTESTLAB_CONTROL_TOKEN` for authenticated control APIs

Use `lab.Check(ctx)` for non-fatal assertions (`t.Error`) and `lab.Expect(ctx)` for fatal
assertions (`t.Fatal`).

## Query calls

```go
trace := lab.Wait(ctx)
chatCalls := trace.Query().
    Upstream("openai").
    APIType("chat").
    Model("gpt-4.1-mini").
    Status(200).
    Calls()
```

Other filters:

- `Errors()`
- `Retries()`
- `Index(i)`

## Tool assertions

```go
assert.ToolCalled("get_weather")
assert.ToolNotCalled("delete_user")
assert.ToolCalledTimes("get_weather", 1)
assert.ToolCalled("get_weather",
    llmtestlab.ArgsSubset(map[string]any{"city": "Paris"}),
    llmtestlab.ArgPath("unit", "celsius"),
    llmtestlab.ArgsMatch(func(raw json.RawMessage) error { return nil }),
)
assert.ToolOrder("get_weather", "summarize")
assert.ToolBefore("get_weather", "summarize")
assert.ParallelToolCalls("lookup_a", "lookup_b")
assert.ToolResultFedBack("call_abc123")
```

Argument matching is JSON based:

- `ArgsSubset` recursively checks that expected keys/values are present.
- `ArgPath` supports simple dot paths and numeric array indexes, e.g. `items.0.id`.
- `ArgsMatch` accepts a custom matcher function.

## Metadata assertions

```go
assert.Model("gpt-4.1-mini")
assert.RequestParam("temperature", 0)
assert.FinishReason("tool_calls")
assert.TokensBelow(1000)
assert.TokensAtMost(1000)
assert.LatencyBelow(2 * time.Second)
assert.TTFTBelow(500 * time.Millisecond)
assert.CallCount(1, llmtestlab.ExcludeRetries())
assert.NoErrors()
assert.HasError()
assert.MidStreamError()
assert.SystemPromptContains("You are")
```

## Response content assertions

```go
assert.ResponseContains("done")
assert.ResponseMatches(`(?i)success`)
assert.ResponseJSONValid()
assert.ResponseJSONSchemaValid(map[string]any{
    "type": "object",
    "required": []any{"answer"},
    "properties": map[string]any{
        "answer": map[string]any{"type": "string"},
    },
})
```

The schema validator is intentionally small and dependency-free. It supports `type`,
`required`, and nested `properties` for common structured-output tests.

## Failure output

Failures include a compact trace summary:

```text
expected tool "get_weather" to be called

Trace summary:
session 9f...: 2 call(s)
  #1 openai openai.chat status=200 finish=tool_calls model=gpt-4.1-mini/gpt-4.1-mini latency=730ms retry=0
    tool[0] get_weather args={"city":"Paris"}
```

The package also keeps the lower-level client methods (`Calls`, `Wait`, `OTLP`, `Delete`)
for custom assertions.
