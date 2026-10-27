# Go quickstart

Five minutes from zero to a passing trace assertion in `go test`.
Versions on this page: proxy **v0.5.3**, [llmentary-go](https://github.com/barzoj-co/llmentary-go) **v0.5.4**.

## 1. Install the proxy

Download an archive from [GitHub Releases](https://github.com/barzoj-co/llmentary/releases)
or build it from source:

```bash
go install github.com/barzoj-co/llmentary/cmd/llmentary@v0.5.3
```

Put it on your `PATH` as `llmentary`, or set `LLMENTARY_BIN`. Archive names and
signature verification: [Release binaries](../binaries.md).

## 2. Add the client

```bash
go get github.com/barzoj-co/llmentary-go
```

## 3. Start the proxy

With no `--upstream`, it forwards to `openai=https://api.openai.com/v1`:

```bash
llmentary \
  --listen 127.0.0.1:7777 \
  --control 127.0.0.1:7778
```

## 4. Write a test

`llmentary.New(t)` creates a session for this test and tags requests with
`X-Test-Session`:

```go
package myapp_test

import (
    "context"
    "os"
    "testing"

    "github.com/barzoj-co/llmentary-go"
    "github.com/openai/openai-go"
    "github.com/openai/openai-go/option"
)

func TestWeatherTool(t *testing.T) {
    lm := llmentary.New(t)

    client := openai.NewClient(
        option.WithAPIKey(os.Getenv("OPENAI_API_KEY")),
        option.WithBaseURL(lm.OpenAIBaseURL("")), // http://127.0.0.1:7777/v1
        option.WithHTTPClient(lm.HTTPClient()),   // injects X-Test-Session
        option.WithMaxRetries(0),
    )

    _, err := client.Chat.Completions.New(context.Background(), openai.ChatCompletionNewParams{
        Model: "gpt-4.1-mini",
        Messages: []openai.ChatCompletionMessageParamUnion{
            openai.UserMessage("Weather in Paris?"),
        },
        Tools: []openai.ChatCompletionToolParam{{
            Function: openai.FunctionDefinitionParam{
                Name: "get_weather",
                Parameters: openai.FunctionParameters{
                    "type": "object",
                    "properties": map[string]any{
                        "city": map[string]any{"type": "string"},
                    },
                    "required": []string{"city"},
                },
            },
        }},
        ToolChoice: openai.ChatCompletionToolChoiceOptionParamOfChatCompletionNamedToolChoice(
            openai.ChatCompletionNamedToolChoiceFunctionParam{Name: "get_weather"},
        ),
    })
    if err != nil {
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

## 5. Run it

```bash
OPENAI_API_KEY=sk-... go test ./...
```

`lm.Expect(ctx)` waits until the session is quiet, then fails the test with
`t.Fatal` and a trace summary. `lm.Check(ctx)` is the same API with `t.Error`.

## Next steps

- **Service can't send headers?** Set the SDK base URL to `lm.PathBaseURL("")`
  (`http://127.0.0.1:7777/s/<id>/v1`) **before** the app is constructed:

    ```go
    func TestService(t *testing.T) {
        lm := llmentary.New(t)
        t.Setenv("OPENAI_BASE_URL", lm.PathBaseURL(""))
        // construct or import the service after this line
    }
    ```

    More in [Black-box and sidecar setups](../black-box.md).

- **No live provider?** `llmentary.StartForTest(t, ...)` starts the proxy from
  the test; see
  [`examples/go/weather_test.go`](https://github.com/barzoj-co/llmentary-go/blob/main/examples/go/weather_test.go).
- **Scope one turn** with `lm.Mark(ctx)` and `llmentary.Since(mark)`.
- Full API: [Go client reference](https://github.com/barzoj-co/llmentary-go/blob/main/docs/go-client.md)
  and [pkg.go.dev](https://pkg.go.dev/github.com/barzoj-co/llmentary-go).
