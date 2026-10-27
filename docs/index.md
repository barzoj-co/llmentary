---
title: llmentary
description: Integration tests for LLM apps that assert on the recorded requests and responses, not on a judge model's opinion.
hide:
  - navigation
  - toc
---

<div class="lm-hero" markdown>

# llmentary

Integration tests for LLM applications. Your code calls the real provider
through a local recording proxy, and your test asserts on what was sent and
received: which tools were offered and called, with which arguments, how many
calls, tokens, latency, errors.

[Get started](getting-started/python.md){ .md-button .md-button--primary }
[GitHub](https://github.com/barzoj-co/llmentary){ .md-button }

</div>

## Example

```python
def test_model_calls_weather_tool(llmentary):
    client = OpenAI(
        api_key=os.environ["OPENAI_API_KEY"],
        base_url=llmentary.openai_base_url,
        default_headers=llmentary.headers,
    )
    client.chat.completions.create(
        model="gpt-4.1-mini",
        messages=[{"role": "user", "content": "What's the weather in Paris?"}],
        tools=[WEATHER_TOOL],
    )

    llmentary.expect().call_count(1).args_subset("get_weather", {"city": "Paris"})
```

Change the expected city to `"Berlin"` and the test fails with the recorded
trace:

```text
AssertionError: expected a 'get_weather' call with argument subset {'city': 'Berlin'}

llmentary session test_model_calls_weather_tool-1a69…: 1 captured call(s)
  #1 (call_1) api=chat_completions model=gpt-4.1-mini status=ok, http=200,
     finish=tool_calls, latency=1410.0ms, src=header, tokens=43/14 tools=[get_weather]
```

The same test in [Go](getting-started/go.md) and
[TypeScript](getting-started/typescript.md).

## Why assert on the trace

The model's wording changes from run to run. The structure of the exchange
usually does not: the tools your app offered, the tool the model chose, the
arguments, the tool result your app sent back, the request parameters, the
number of retries. Those are recorded byte for byte, so assertions on them
pass or fail for a reason you can read in the diff. There is no second model
grading the first.

For the parts that are free text, see
[Assert on model output](how-to/model-output.md).

## Install

| Language | Package | Proxy binary |
|---|---|---|
| Python (pytest) | `pip install "llmentary[pytest,openai]"` | included in the wheel |
| TypeScript (Vitest, Jest) | `npm install --save-dev llmentary` | included via an optional platform package |
| Go (`go test`) | `go get github.com/barzoj-co/llmentary-go` | install separately: [Release binaries](binaries.md) |

Works with the OpenAI SDKs and any OpenAI-compatible endpoint
([providers](providers.md)). Chat Completions and Responses API, streaming and
non-streaming.

## Documentation

<div class="grid cards" markdown>

-   **Get started**

    ---

    Write and run a first test in [Python](getting-started/python.md),
    [TypeScript](getting-started/typescript.md) or [Go](getting-started/go.md).

-   **How-to guides**

    ---

    [Connect an existing app](how-to/connect-an-app.md),
    [test one turn of a conversation](how-to/scope-a-turn.md),
    [run in CI](ci.md).

-   **Concepts**

    ---

    [How llmentary works](concepts.md): the proxy, sessions, waiting,
    call outcomes.

-   **Reference**

    ---

    [Assertions](reference/assertions.md),
    [proxy flags and control API](proxy.md),
    [recorded data model](data-model.md).

</div>

## License

The proxy is AGPL-3.0. The Go, Python and TypeScript clients are MIT.
