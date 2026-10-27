# Get started with Go

This page takes you from an empty module to a passing `go test` that calls
OpenAI through llmentary and checks which tool the model called.

You need Go 1.22 or newer and an OpenAI API key. The test makes one real request
to `gpt-4.1-mini`.

## Install the proxy

The Go client does not bundle the proxy. Install it with `go install`, or
download an archive from [GitHub Releases](https://github.com/barzoj-co/llmentary/releases)
([archive names and verification](../binaries.md)):

```bash
go install github.com/barzoj-co/llmentary/cmd/llmentary@v0.5.3
```

Building the proxy needs Go 1.26. With the default `GOTOOLCHAIN=auto`, an older
`go` command downloads that toolchain for the build.

Start it in a separate terminal:

```console
$ llmentary --listen 127.0.0.1:7777 --control 127.0.0.1:7778
INFO no upstream configured; defaulting to openai=https://api.openai.com/v1
```

Port 7777 is the proxy your SDK talks to. Port 7778 is the control API the test
client uses to read recorded calls. These are the addresses the Go client uses
by default.

## Add the client

```bash
go mod init example.com/weather
go get github.com/barzoj-co/llmentary-go github.com/openai/openai-go@v1.12.0
```

## Write a test

```go
// weather_test.go
package weather_test

import (
	"context"
	"os"
	"testing"

	"github.com/barzoj-co/llmentary-go"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

var weatherTool = openai.ChatCompletionToolParam{
	Function: openai.FunctionDefinitionParam{
		Name: "get_weather",
		Parameters: openai.FunctionParameters{
			"type":       "object",
			"properties": map[string]any{"city": map[string]any{"type": "string"}},
			"required":   []string{"city"},
		},
	},
}

func TestModelCallsWeatherTool(t *testing.T) {
	ctx := context.Background()
	lm := llmentary.New(t)

	client := openai.NewClient(
		option.WithAPIKey(os.Getenv("OPENAI_API_KEY")),
		option.WithBaseURL(lm.OpenAIBaseURL("")),
		option.WithHTTPClient(lm.HTTPClient()),
		option.WithMaxRetries(0),
	)
	_, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model:      "gpt-4.1-mini",
		Messages:   []openai.ChatCompletionMessageParamUnion{openai.UserMessage("What's the weather in Paris?")},
		Tools:      []openai.ChatCompletionToolParam{weatherTool},
		ToolChoice: openai.ChatCompletionToolChoiceOptionUnionParam{OfAuto: openai.String("required")},
	})
	if err != nil {
		t.Fatal(err)
	}

	check := lm.Expect(ctx)
	check.CallCount(1)
	check.ToolCalled("get_weather", llmentary.ArgsSubset(map[string]any{"city": "Paris"}))
	check.NoErrors()
	check.TokensBelow(1000)
}
```

`llmentary.New(t)` creates a session with a unique id and deletes it when the
test ends. `lm.HTTPClient()` adds the `X-Test-Session` header to every request,
which tells the proxy which session the request belongs to. The API key is
passed through to OpenAI unchanged.

`lm.Expect(ctx)` waits until the session has had no new calls for 500 ms,
fetches the recorded calls and returns the assertions. A failed assertion calls
`t.Fatal` with the trace. `lm.Check(ctx)` returns the same assertions but
reports failures with `t.Error`, so the test continues.

## Run it

```console
$ export OPENAI_API_KEY=sk-...
$ go test ./...
ok  	example.com/weather	2.155s
```

## See a failure

Change the expected city to `"Berlin"`:

```text
--- FAIL: TestModelCallsWeatherTool (1.32s)
    weather_test.go:46: expected tool "get_weather" to be called
        Trace summary:
        session 0b71356f…: 1 call(s)
          #1 (call_2) openai openai.chat status=200 finish=tool_calls model=gpt-4.1-mini/gpt-4.1-mini-2025-04-14 latency=817ms retry=0 spec=clean src=header tokens=43/14
            tool[0] get_weather args={"city":"Paris"}
```

## Start the proxy from the test

To avoid a separate terminal, start the proxy from the test with
`llmentary.StartForTest`. It looks for the binary in `LLMENTARY_BIN`, then on
`PATH`:

```go
proxy := llmentary.StartForTest(t)
lm := llmentary.New(t,
    llmentary.WithProxyURL(proxy.ProxyURL),
    llmentary.WithControlURL(proxy.ControlURL),
)
```

## Next steps

- [Connect an existing app](../how-to/connect-an-app.md) when the OpenAI client
  is created inside your application code.
- [Test one turn of a conversation](../how-to/scope-a-turn.md).
- [Assertions reference](../reference/assertions.md).
- Go API details: [pkg.go.dev](https://pkg.go.dev/github.com/barzoj-co/llmentary-go).
