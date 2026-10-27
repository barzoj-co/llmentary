# Get started with Python

This page takes you from an empty directory to a passing pytest test that
calls OpenAI through llmentary and checks which tool the model called. It takes
about five minutes.

You need Python 3.9 or newer and an OpenAI API key. The test makes one real
request to `gpt-4.1-mini`.

## Install

```bash
python -m venv .venv
source .venv/bin/activate
pip install "llmentary[pytest,openai]"
```

The wheel contains the proxy binary for your platform. Check that it was found:

```console
$ python -m llmentary doctor
llmentary 0.7.3
bundled proxy: 0.5.3
binary: …/site-packages/llmentary/bin/manylinux2014_x86_64/llmentary
…
```

## Write a test

Create `test_weather.py`:

```python
import os
from openai import OpenAI

WEATHER_TOOL = {
    "type": "function",
    "function": {
        "name": "get_weather",
        "parameters": {
            "type": "object",
            "properties": {"city": {"type": "string"}},
            "required": ["city"],
        },
    },
}


def test_model_calls_weather_tool(llmentary):
    client = OpenAI(
        api_key=os.environ["OPENAI_API_KEY"],
        base_url=llmentary.openai_base_url,
        default_headers=llmentary.headers,
        max_retries=0,
    )
    client.chat.completions.create(
        model="gpt-4.1-mini",
        messages=[{"role": "user", "content": "What's the weather in Paris?"}],
        tools=[WEATHER_TOOL],
        tool_choice="required",
    )

    (
        llmentary.expect()
        .call_count(1)
        .tool_called("get_weather")
        .args_subset("get_weather", {"city": "Paris"})
        .no_errors()
        .tokens(max_total=1000)
    )
```

The `llmentary` fixture comes from the pytest plugin that the package
registers. For each test it:

1. starts the proxy once per pytest run (on `127.0.0.1`, on free ports),
2. creates a session with a unique id,
3. gives you `openai_base_url` and `headers`. The `X-Test-Session` header tells
   the proxy which session a request belongs to.

The proxy forwards the request to `https://api.openai.com/v1` and records the
request and response. The SDK's API key is passed through unchanged, so it has
to be a real key.

`llmentary.expect()` waits until the session has had no new calls for 500 ms,
fetches the recorded calls and returns an object you chain assertions on.

## Run it

```console
$ export OPENAI_API_KEY=sk-...
$ pytest -q test_weather.py
.                                                                        [100%]
1 passed in 2.49s
```

## See a failure

Change the expected city to `"Berlin"` and run again:

```text
E   AssertionError: expected a 'get_weather' call with argument subset {'city': 'Berlin'}
E   llmentary session test_model_calls_weather_tool-1a69…: 1 captured call(s)
E     #1 (call_1) api=chat_completions model=gpt-4.1-mini status=ok, http=200,
E        finish=tool_calls, latency=1410.0ms, src=header, tokens=43/14 tools=[get_weather]
```

The summary under the message is the recorded trace for this test.

## Next steps

- [Connect an existing app](../how-to/connect-an-app.md) when the OpenAI client
  is created inside your application code.
- [Test one turn of a conversation](../how-to/scope-a-turn.md).
- [Assertions reference](../reference/assertions.md).
- Python API details: [llmentary-python docs](https://github.com/barzoj-co/llmentary-python/blob/main/docs/api.md).
