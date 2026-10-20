# Troubleshooting

## `configure at least one upstream`

This error is gone. With no `--upstream` and no `LLMENTARY_UPSTREAMS` /
`LLMENTARY_UPSTREAM`, the proxy defaults to `openai=https://api.openai.com/v1` and
logs `no upstream configured; defaulting to openai=https://api.openai.com/v1`.

To set a different provider:

```bash
llmentary --upstream openrouter=https://openrouter.ai/api/v1
```

Or:

```bash
export LLMENTARY_UPSTREAMS='openai=https://api.openai.com/v1'
llmentary
```

`LLMENTARY_UPSTREAM` is accepted as a single-value fallback when `LLMENTARY_UPSTREAMS`
is empty.

## `upstream ... points at the proxy listen address ... refusing to start a proxy loop`

The configured upstream host:port is the proxy itself (`localhost`, `127.0.0.1`,
`0.0.0.0`, or `[::1]` with the same port as `--listen`). Point `--upstream` at the
real provider, not at the proxy URL. Do not set the upstream from `OPENAI_BASE_URL`
when that variable already points at llmentary.

## `control API on non-loopback address ... requires --control-token`

The control API exposes raw recorded request/response bodies. Non-loopback binds require
a token.

```bash
llmentary \
  --control 0.0.0.0:7778 \
  --control-token "$LLMENTARY_CONTROL_TOKEN" \
  --upstream openai=https://api.openai.com/v1
```

Clients must send:

```http
Authorization: Bearer <token>
```

The [Go client](https://github.com/barzoj-co/llmentary-go) reads
`LLMENTARY_CONTROL_TOKEN` automatically or can be configured with
`llmentary.WithControlToken(token)`.

## `401 unauthorized` from the control API

The control server was started with a control token, but the client did not send the
matching bearer token.

Check:

```bash
echo "$LLMENTARY_CONTROL_TOKEN"
curl -H "Authorization: Bearer $LLMENTARY_CONTROL_TOKEN" \
  http://127.0.0.1:7778/sessions/default
```

## `unknown upstream`

The URL path uses `/u/{name}/...`, but `{name}` is not configured.

```text
GET /u/openai/chat/completions
```

requires:

```bash
--upstream openai=https://api.openai.com/v1
```

Names are trimmed of leading/trailing slashes.

## Bare proxy paths return `502 Bad Gateway`

Bare paths such as `/v1/chat/completions` only work when:

- exactly one upstream is configured, or
- an upstream named `default` is configured.

With multiple named upstreams, use `/u/{name}/...`:

```text
http://127.0.0.1:7777/u/openai/chat/completions
```

## No calls show up in my test session

Check session tagging:

- `X-Test-Session` wins and is stripped before forwarding upstream.
- W3C baggage key `llmentary.session.id` is accepted when the header is absent.
- Calls with no tag land in the `default` session.

For Go tests, prefer the [Go client](https://github.com/barzoj-co/llmentary-go):

```go
lm := llmentary.New(t)
client := openai.NewClient(
    option.WithBaseURL(lm.OpenAIBaseURL("openai")),
    option.WithHTTPClient(lm.HTTPClient()),
)
```

Or set the header manually:

```go
key, value := lm.Header()
req.Header.Set(key, value)
```

## `Wait` times out

`GET /sessions/{id}/wait` waits until the session has `min_calls` finished calls, no
active calls, and has been quiet for the requested duration. A session that does not
exist yet is not an empty `200`: `min_calls=0` returns `504 wait_timeout` after
`quiet`. Long streams, hung clients, or too-small timeouts can also cause `504`.
Unknown-id `GET /sessions/{id}` is `404 session_not_found` (GETs never create
sessions). List known ids with `GET /sessions`.

Try:

```go
lm := llmentary.New(t, llmentary.WithWait(100*time.Millisecond, 60*time.Second)) // llmentary-go
```

Or inspect the current session without waiting:

```bash
curl http://127.0.0.1:7778/sessions/<id>
```

## A call is `api_family: "passthrough"`

Only these API shapes are parsed:

- `POST` paths ending with `/chat/completions`
- `POST` paths ending with `/responses`

Everything else is forwarded and recorded as raw passthrough. Check the final upstream
path after the configured upstream base path and `/u/{name}` prefix are joined.

## Streaming usage is missing

Usage is recorded only when the provider sends usage in the response body, a Chat
Completions final stream chunk, or a Responses terminal event.

LangChain Python disables streaming usage for custom `base_url` unless you set:

```python
stream_usage=True
```

## The stream is marked truncated

For Chat Completions, the stream is complete only when the parser sees:

```text
data: [DONE]
```

For Responses, the stream is complete only when the parser sees `response.completed`.

If the client closes the response early, `normalized.client_aborted` can also be set.

## Azure content-filter chunks look odd

Azure can send stream chunks with empty `choices` for prompt content-filter metadata. The
parser tolerates those chunks and continues reading later choices/tool-call deltas. Parsed
per-choice filter results appear under `normalized.content_filter`.

## `openai-go` refuses Azure credentials over HTTP

A loopback proxy URL such as `http://127.0.0.1:7777/u/azure` is acceptable for local tests.
If your test environment reaches the proxy over a non-loopback plain HTTP hostname,
`openai-go` Azure credential safety checks can refuse the request. Use a loopback bind,
an OpenAI-compatible Azure `/openai/v1` client configuration, or wait for a future TLS
listener.

## Why is `--tls` not enabling HTTPS?

The current CLI registers a `--tls` boolean flag, but the server code does not use it to
start a TLS listener. Keep control and proxy listeners on trusted loopback interfaces for
now.

## Raw bodies contain sensitive data

Recorded headers and selected URL query parameters are redacted, but raw request and
response bodies are stored in memory for assertions. Do not expose the control API to
untrusted networks or users.

## Gzip responses

The proxy forwards the client's `Accept-Encoding` unchanged (it does not force
`identity`). If an upstream returns gzip, deflate, brotli, or zstd, the SDK receives
those bytes unchanged and the recorded copy is decompressed for parsing. Unknown
encodings are stored raw and a WARN is logged.

## Real-provider e2e tests skip locally

The e2e test file has the `e2e` build tag and skips when `OPENAI_API_KEY` is unset:

```bash
OPENAI_API_KEY=sk-... go test -v -tags=e2e ./...
```
