# llmentary

[![CI](https://github.com/barzoj-co/llmentary/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/barzoj-co/llmentary/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/barzoj-co/llmentary/graph/badge.svg)](https://codecov.io/gh/barzoj-co/llmentary)
[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/barzoj-co/llmentary/badge)](https://scorecard.dev/viewer/?uri=github.com/barzoj-co/llmentary)
[![License: AGPL-3.0](https://img.shields.io/github/license/barzoj-co/llmentary)](LICENSE)
[![Release](https://img.shields.io/github/v/release/barzoj-co/llmentary?sort=semver)](https://github.com/barzoj-co/llmentary/releases)

llmentary is a recording proxy for testing LLM applications. Point your OpenAI
SDK at it, run your code against the real provider, and assert in your test on
what was sent and received: tools offered and called, arguments, request
parameters, number of calls, tokens, latency and errors. Clients are available
for pytest, Vitest/Jest and `go test`.

Documentation: **https://barzoj-co.github.io/llmentary/**

## Install

The Python and TypeScript packages include the proxy binary:

```bash
pip install "llmentary[pytest,openai]"     # Python
npm install --save-dev llmentary           # TypeScript
```

For Go, install the proxy and the client separately:

```bash
go install github.com/barzoj-co/llmentary/cmd/llmentary@latest
go get github.com/barzoj-co/llmentary-go
```

Release archives and signature verification: [docs/binaries.md](docs/binaries.md).

## Example

```python
def test_weather(llmentary):
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

The same test with the other clients: `lm := llmentary.New(t)` in
[Go](https://barzoj-co.github.io/llmentary/getting-started/go/),
`const lm = llmentary()` from `import { llmentary } from "llmentary/vitest"`
(or `const { llmentary } = require("llmentary/jest")`) in
[TypeScript](https://barzoj-co.github.io/llmentary/getting-started/typescript/).

## What you can assert

- Tool calls: which tools were called or not, arguments, order, and whether
  your app sent the results back.
- Requests: model, parameters, prompt and system prompt content.
- Reliability: call count, retries, HTTP errors, broken streams, cancelled
  requests.
- Budgets: token usage, latency, time to first token.
- Isolation between concurrent sessions
  ([Isolation spec](docs/isolation-spec.md), [test vectors](testdata/isolation/)),
  and request validity against the OpenAI schema, including
  [OpenAI-compatible providers](docs/openai-compatible.md).

## Documentation

- [Get started](https://barzoj-co.github.io/llmentary/getting-started/python/)
- [Assertions reference](https://barzoj-co.github.io/llmentary/reference/assertions/)
- [Proxy flags and control API](docs/proxy.md)
- [Contributing](CONTRIBUTING.md), [Security](SECURITY.md)

<!-- llmentary-repos:start -->
## llmentary repositories

| Repository | What | License |
|---|---|---|
| [llmentary](https://github.com/barzoj-co/llmentary) | Recording proxy binary (Go) | AGPL-3.0 |
| [llmentary-go](https://github.com/barzoj-co/llmentary-go) | Go client for `go test` | MIT |
| [llmentary-python](https://github.com/barzoj-co/llmentary-python) | Python client and pytest plugin | MIT |
| [llmentary-js](https://github.com/barzoj-co/llmentary-js) | TypeScript/JavaScript client for Vitest and Jest | MIT |
<!-- llmentary-repos:end -->

## License

The proxy is licensed under [AGPL-3.0](LICENSE). The client libraries are MIT.
