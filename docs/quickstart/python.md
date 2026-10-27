# Python quickstart (pytest)

Five minutes from zero to a passing trace assertion in pytest.
Versions on this page: [llmentary for Python](https://github.com/barzoj-co/llmentary-python)
**0.7.3**, which bundles proxy **v0.5.3** in its platform wheels.

## 1. Install

```bash
pip install "llmentary[pytest,openai]"
```

Platform wheels (`manylinux2014_x86_64`, `manylinux2014_aarch64`,
`macosx_11_0_x86_64`, `macosx_11_0_arm64`, `win_amd64`) bundle the proxy binary.
The sdist is client-only: on other platforms set `LLMENTARY_BIN` or put
`llmentary` on `PATH`. Check what resolved with:

```bash
python -m llmentary doctor
```

## 2. Write a test

The plugin registers a function-scoped fixture named `llmentary`. It creates a
unique session, exposes OpenAI client helpers, waits for quiet before
assertions, and deletes the session after the test.

```python
from openai import OpenAI


def test_tool_flow(llmentary):
    client = OpenAI(
        api_key="test-key",
        base_url=llmentary.openai_base_url,
        default_headers=llmentary.headers,
    )

    first = client.chat.completions.create(
        model="gpt-4.1-mini",
        messages=[{"role": "user", "content": "Weather in Paris?"}],
        tools=[
            {
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
        ],
    )
    tool_call = first.choices[0].message.tool_calls[0]

    client.chat.completions.create(
        model="gpt-4.1-mini",
        messages=[
            {"role": "user", "content": "Weather in Paris?"},
            first.choices[0].message,
            {"role": "tool", "tool_call_id": tool_call.id, "content": '{"weather":"sunny"}'},
        ],
    )

    llmentary.expect().call_count(2).tool_called("get_weather").args_subset(
        "get_weather", {"city": "Paris"}
    ).tool_result_fed_back("get_weather").no_errors()
```

## 3. Run it

Let the session-scoped `llmentary_proxy` fixture start the proxy and give it
the upstream key:

```bash
export LLMENTARY_UPSTREAM_BASE_URL=https://api.openai.com/v1
export LLMENTARY_UPSTREAM_API_KEY="$OPENAI_API_KEY"
pytest
```

Or attach to a proxy you already run:

```bash
export LLMENTARY_PROXY_URL=http://127.0.0.1:7777
export LLMENTARY_CONTROL_URL=http://127.0.0.1:7778
pytest
```

All options and environment variables are in the
[pytest plugin docs](https://github.com/barzoj-co/llmentary-python/blob/main/docs/pytest.md#options-and-environment).

## Next steps

- **Import-time client that can't send headers?** Set `OPENAI_BASE_URL` to
  `llmentary.path_base_url()` **inside the test**, then import the app:

    ```python
    import importlib


    def test_import_time_client(llmentary, monkeypatch):
        monkeypatch.setenv("OPENAI_BASE_URL", llmentary.path_base_url())
        app = importlib.import_module("myapp")
        app.client.chat.completions.create(
            model="gpt-4.1-mini",
            messages=[{"role": "user", "content": "ping"}],
        )
        llmentary.expect().prompt_contains("ping").no_errors()
    ```

    More in [Black-box and sidecar setups](../black-box.md).

- Full API: [Python API reference](https://github.com/barzoj-co/llmentary-python/blob/main/docs/api.md)
  and [pytest plugin](https://github.com/barzoj-co/llmentary-python/blob/main/docs/pytest.md).
