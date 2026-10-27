---
title: llmentary
description: Programmatic, deterministic behavior and contract tests for LLM apps. Assertions in code, not an LLM judge.
hide:
  - navigation
  - toc
---

<div class="lm-hero" markdown>

# Test what your LLM app actually sent and received

**llmentary** gives you programmatic, deterministic behavior and contract tests
for LLM apps: assertions in code on the recorded trace, instead of an LLM judge.

[Get started](quickstart/go.md){ .md-button .md-button--primary }
[View on GitHub](https://github.com/barzoj-co/llmentary){ .md-button }

</div>

## One test, real SDK, real provider

Put the llmentary proxy between your app and an OpenAI-compatible provider, run
your real code, then assert on what went over the wire:

=== "Go"

    ```go
    func TestWeatherTool(t *testing.T) {
        lm := llmentary.New(t)

        client := openai.NewClient(
            option.WithAPIKey(os.Getenv("OPENAI_API_KEY")),
            option.WithBaseURL(lm.OpenAIBaseURL("openai")), // http://127.0.0.1:7777/u/openai
            option.WithHTTPClient(lm.HTTPClient()),         // injects X-Test-Session
            option.WithMaxRetries(0),
        )

        // Drive your real app/agent with the client above.
        if err := RunWeatherAgent(context.Background(), client, "Weather in Paris?"); err != nil {
            t.Fatal(err)
        }

        assert := lm.Expect(context.Background())
        assert.CallCount(1)
        assert.ToolCalled("get_weather", llmentary.ArgsSubset(map[string]any{"city": "Paris"}))
        assert.NoRetries()
        assert.NoErrors()
        assert.TokensBelow(1000)
    }
    ```

=== "Python (pytest)"

    ```python
    def test_tool_flow(llmentary):
        client = OpenAI(
            api_key=os.environ["OPENAI_API_KEY"],
            base_url=llmentary.openai_base_url,
            default_headers=llmentary.headers,
        )
        # ... drive your app with this client ...

        llmentary.expect().call_count(2).tool_called("get_weather").args_subset(
            "get_weather", {"city": "Paris"}
        ).tool_result_fed_back("get_weather").no_errors()
    ```

=== "TypeScript (Vitest)"

    ```ts
    test("uses the weather tool", async () => {
      const lm = llmentary();
      const openai = new OpenAI(lm.openAI());
      // ... drive your app with this client ...

      await lm.expect().toolCalled("lookup_weather", { argsSubset: { city: "Amsterdam" } });
    });
    ```

The assertion waits until the session is quiet, then checks the recorded calls.
No second model grades the first one.

## How it works

<div class="grid cards" markdown>

-   :material-swap-horizontal:{ .lg .middle } **1. Proxy in the middle**

    ---

    `llmentary` is a Go reverse proxy. Point an OpenAI-compatible SDK at it;
    it forwards to the real provider and records every call per test session.

-   :material-play-circle-outline:{ .lg .middle } **2. Run your real code**

    ---

    Your app or agent makes its normal SDK calls. Sessions are attributed by
    the `X-Test-Session` header, or by a `/s/{session}/` path prefix for
    unmodified services.

-   :material-check-decagram-outline:{ .lg .middle } **3. Assert in code**

    ---

    Assert on the trace from `go test`, pytest, Vitest or Jest: tools, arguments,
    order, request params, finish reasons, tokens, retries, latency, TTFT and
    provider errors.

</div>

## What you can assert

| Area | Examples |
|---|---|
| Tools | `ToolCalled` / `ToolNotCalled` / `ToolOrder`, `ArgsSubset`, tool results fed back |
| Calls | `CallCount`, request parameters, model, finish reason |
| Reliability | `NoRetries`, `NoErrors`, HTTP status, client aborts |
| Budgets | `TokensBelow`, `LatencyBelow`, `TTFTBelow` |
| Spec | `NoSpecViolations`, `NoIgnoredFields` against the OpenAI spec |
| Isolation | `IsolatedFrom`: prove two sessions never saw each other's turns |

Trace structure is deterministic; the model's free text is not. Read
[Assertions: trace vs free text](assertions-guide.md) for where to draw the line.

## Pick your language

<div class="grid cards" markdown>

-   :fontawesome-brands-golang:{ .lg .middle } **Go**

    ---

    `go get github.com/barzoj-co/llmentary-go`

    [:octicons-arrow-right-24: Go quickstart](quickstart/go.md)

-   :fontawesome-brands-python:{ .lg .middle } **Python**

    ---

    `pip install "llmentary[pytest,openai]"`

    [:octicons-arrow-right-24: pytest quickstart](quickstart/python.md)

-   :material-language-typescript:{ .lg .middle } **TypeScript**

    ---

    `npm install --save-dev llmentary`

    [:octicons-arrow-right-24: Vitest / Jest quickstart](quickstart/typescript.md)

</div>

## Repositories

| Repository | What | License |
|---|---|---|
| [llmentary](https://github.com/barzoj-co/llmentary) | Recording proxy binary (Go) | AGPL-3.0 |
| [llmentary-go](https://github.com/barzoj-co/llmentary-go) | Go client for `go test` | MIT |
| [llmentary-python](https://github.com/barzoj-co/llmentary-python) | Python client and pytest plugin | MIT |
| [llmentary-js](https://github.com/barzoj-co/llmentary-js) | TypeScript/JavaScript client for Vitest and Jest | MIT |
