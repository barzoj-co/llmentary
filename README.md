# llmentary

Deterministic, trace-based integration testing for LLM apps, without an LLM judge.

[![CI](https://github.com/barzoj-co/llmentary/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/barzoj-co/llmentary/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/barzoj-co/llmentary/graph/badge.svg)](https://codecov.io/gh/barzoj-co/llmentary)
[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/barzoj-co/llmentary/badge)](https://scorecard.dev/viewer/?uri=github.com/barzoj-co/llmentary)
[![License: AGPL-3.0](https://img.shields.io/github/license/barzoj-co/llmentary)](LICENSE)
[![Release](https://img.shields.io/github/v/release/barzoj-co/llmentary?sort=semver)](https://github.com/barzoj-co/llmentary/releases)

`llmentary` is a Go reverse proxy. Put it between your app and an OpenAI-compatible
provider, run the real SDK call, then assert on the recorded trace: tool names, tool
arguments, tool order, request parameters, finish reasons, token usage, retries,
latency, TTFT, and provider errors. Clients live in separate repositories and
download or run this proxy binary.

## Install

Install the proxy from a [GitHub Release](https://github.com/barzoj-co/llmentary/releases),
or from source:

```bash
go install github.com/barzoj-co/llmentary/cmd/llmentary@latest
```

How release archives are named and verified is in [Binaries](docs/binaries.md).

## Quickstart

Start the proxy against OpenAI:

```bash
llmentary \
  --listen 127.0.0.1:7777 \
  --control 127.0.0.1:7778 \
  --upstream openai=https://api.openai.com/v1
```

Then drive a real SDK call through the [Go client](https://github.com/barzoj-co/llmentary-go):

```go
package myapp_test

import (
    "context"
    "os"
    "testing"

    llmentary "github.com/barzoj-co/llmentary-go"
    "github.com/openai/openai-go"
    "github.com/openai/openai-go/option"
)

func TestWeatherTool(t *testing.T) {
    lm := llmentary.New(t)

    client := openai.NewClient(
        option.WithAPIKey(os.Getenv("OPENAI_API_KEY")),
        option.WithBaseURL(lm.OpenAIBaseURL("openai")), // http://127.0.0.1:7777/u/openai
        option.WithHTTPClient(lm.HTTPClient()),         // injects X-Test-Session
        option.WithMaxRetries(0),
    )

    // Drive your real app/agent. It should use the OpenAI client above and its
    // normal tool schema/configuration.
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

```bash
OPENAI_API_KEY=sk-... go test ./...
```

## Assertions

`lm.Expect(ctx)` waits until the session is quiet, then asserts on the recorded
trace. Common checks: `CallCount`, `ToolCalled` / `ToolNotCalled` / `ToolOrder`,
`ArgsSubset`, `NoRetries`, `NoErrors`, `TokensBelow`, `FinishReason`,
`ResponseContains`, `LatencyBelow`, and `TTFTBelow`. Scope a subset with
`lm.Wait(ctx).Query()` (Go) or `lm.query({...}).expect()` (JS).

Python tests use a pytest fixture named `llmentary`: `def test_weather(llmentary):`.
JS/TS tests `import { llmentary } from "llmentary/vitest"` or
`const { llmentary } = require("llmentary/jest")`, then `const lm = llmentary()`.

## Documentation

- [Proxy configuration and control API](docs/proxy.md)
- [Provider compatibility and SDK wiring](docs/providers.md)
- [Recorded trace data model](docs/data-model.md)
- [OTel GenAI mapping](docs/otel.md)
- [Binaries](docs/binaries.md)
- [Releasing](docs/releasing.md)
- [GitHub Actions / CI](docs/ci.md)
- [Troubleshooting](docs/troubleshooting.md)
- [Contributing](CONTRIBUTING.md)

<!-- ltl-repos:start -->
## llmentary repositories

| Repository | What | License |
|---|---|---|
| [llmentary](https://github.com/barzoj-co/llmentary) | Recording proxy binary (Go) | AGPL-3.0 |
| [llmentary-go](https://github.com/barzoj-co/llmentary-go) | Go client for `go test` | MIT |
| [llmentary-python](https://github.com/barzoj-co/llmentary-python) | Python client and pytest plugin | MIT |
| [llmentary-js](https://github.com/barzoj-co/llmentary-js) | TypeScript/JavaScript client for Vitest and Jest | MIT |
<!-- ltl-repos:end -->

## License

Licensed under [AGPL-3.0](LICENSE).
