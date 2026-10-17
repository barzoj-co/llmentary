# llm-test-lab

Deterministic, trace-based integration testing for LLM apps.

`llm-test-lab` is a small Go reverse proxy that records LLM inference calls while leaving
the wire protocol alone. Point your app's LLM SDK `base_url` at the proxy, run a real
integration test, then assert on the recorded trace: tool calls, arguments, order, token
usage, prompt size, finish reason, retries, and errors.

The project is AGPL-3.0.

## MVP scope

- `cmd/llmtestlab`: HTTP reverse proxy binary.
- OpenAI Chat Completions parser:
  - OpenAI, Azure dated `api-version` URLs, Azure `/openai/v1`, OpenRouter, and other
    OpenAI-compatible providers.
  - Streaming tool-call deltas, SSE keep-alive comments, Azure content-filter chunks,
    final usage chunks, gzip responses, and HTTP-200 stream errors.
- OpenAI Responses parser:
  - Typed SSE events, terminal `response.completed`, function-call items, usage, and
    HTTP-200 stream errors.
- Everything else is raw passthrough: recorded but not parsed. There is no request or
  response format translation.
- In-memory per-session store and control API.
- Importable Go testing client in `pkg/llmtestlab`.

Python and TypeScript clients, packaging, record/replay cassettes, fault injection, OTel
export, and local TLS are intentionally not in this MVP.

## Install and run

```bash
go install github.com/barzoj-co/llm-test-lab/cmd/llmtestlab@latest

llmtestlab \
  --listen :7777 \
  --control 127.0.0.1:7778 \
  --upstream openai=https://api.openai.com/v1 \
  --upstream azure=https://my-resource.openai.azure.com
```

Environment variables are equivalent:

```bash
export LLMTESTLAB_LISTEN=:7777
export LLMTESTLAB_CONTROL_LISTEN=127.0.0.1:7778
export LLMTESTLAB_UPSTREAMS='openai=https://api.openai.com/v1,azure=https://my-resource.openai.azure.com'
llmtestlab
```

The control API binds to `127.0.0.1:7778` by default. If you bind it to a non-loopback
address, set `--control-token` or `LLMTESTLAB_CONTROL_TOKEN`; clients must send
`Authorization: Bearer <token>`.

Named upstream routing uses a path prefix:

```text
http://localhost:7777/u/openai/chat/completions
  -> https://api.openai.com/v1/chat/completions

http://localhost:7777/u/azure/openai/deployments/my-deployment/chat/completions?api-version=2024-10-21
  -> https://my-resource.openai.azure.com/openai/deployments/my-deployment/chat/completions?api-version=2024-10-21
```

If exactly one upstream is configured, bare paths also work:

```text
http://localhost:7777/v1/chat/completions
```

The proxy forwards auth headers untouched, strips `X-Test-Session` before forwarding, and
records redacted headers and URLs. Secret values in `Authorization`, `api-key`,
`x-api-key`, `x-goog-api-key`, cookies, and query params such as `?key=` or `?sig=` are
stored as short SHA-256 hashes.

## Session control API

Calls are grouped by `X-Test-Session`. If that header is absent, the proxy falls back to
W3C `baggage` keys `llmtestlab.session`, `test-session`, or `x-test-session`. Calls with
no session land in the `default` bucket.

Control endpoints live on the control listener:

```bash
curl http://localhost:7778/healthz
curl http://localhost:7778/sessions/my-test
curl 'http://localhost:7778/sessions/my-test/wait?quiet=500ms&timeout=30s'
curl -X DELETE http://localhost:7778/sessions/my-test
```

Each recorded call includes raw request/response bytes (base64 in JSON), raw SSE events,
status, latency, retry count (`x-stainless-retry-count`), upstream request ids, and a
normalized view with model, system prompt hash, declared tools, tool calls, finish reason,
usage, errors, and parser warnings.

## Go test client

```go
client, err := llmtestlab.NewClient("http://localhost:7778")
if err != nil {
    t.Fatal(err)
}

sessionID := client.NewSession()
headerName, headerValue := client.SessionHeader(sessionID)

req, _ := http.NewRequest("POST", "http://localhost:7777/u/openai/chat/completions", body)
req.Header.Set(headerName, headerValue)
req.Header.Set("Authorization", "Bearer "+os.Getenv("OPENAI_API_KEY"))

// Drive your real app or SDK here.

calls, err := client.Wait(context.Background(), sessionID, 500*time.Millisecond, 30*time.Second)
if err != nil {
    t.Fatal(err)
}

llmtestlab.CallCount(t, calls, 1)
llmtestlab.ToolCalled(t, calls, "get_weather", map[string]any{"city": "Paris"})
llmtestlab.ToolOrder(t, calls, "get_weather")
llmtestlab.ToolNotCalled(t, calls, "delete_user")
llmtestlab.TokensBelow(t, calls, 1000)
```

## SDK base URL gotchas

- `openai-python`, `openai-node`, and `openai-go` can use a custom base URL. For named
  upstreams, include the prefix: `http://localhost:7777/u/openai`.
- Azure classic clients append `/openai/deployments/{deployment}/...`; set their endpoint
  to `http://localhost:7777/u/azure` and keep `api-version` in the SDK config.
- `openai-go` Azure middleware refuses Azure credentials over plaintext HTTP except
  loopback when `azure.WithUnsafeAllowHTTP()` is enabled. Docker/CI setups where the
  proxy is not loopback will need TLS in a follow-up version or a plain OpenAI-compatible
  client pointed at Azure `/openai/v1`.
- LangChain `ChatOpenAI` turns off streaming usage when `base_url` / `OPENAI_BASE_URL` is
  custom. Set `stream_usage=True` if you want token usage in Chat Completions streams.
- The proxy sets `Accept-Encoding: identity` upstream so it can record parseable bytes.
  If an upstream still returns gzip, the client stream is passed through unchanged and the
  recorder decompresses its tee copy for parsing.

## Development

```bash
go test ./...
go test -tags=e2e ./...   # skips unless OPENAI_API_KEY is set
```

The e2e test starts a local proxy, calls the real OpenAI Chat Completions API with a tiny
streaming tool-call request, then dogfoods the Go client assertions against the session API.

Release archives are built with GoReleaser for Linux, macOS, and Windows on amd64/arm64
when a `v*` tag is pushed.
