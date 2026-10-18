# Proxy reference

`llmtestlab` starts two HTTP servers:

- the proxy listener, which forwards SDK/provider traffic and records calls;
- the control listener, which exposes recorded sessions and in-memory OTLP spans.

The proxy does not translate request or response formats. It forwards the upstream
request body, response body, and streaming bytes unchanged, while recording a tee copy for
tests.

## CLI flags, environment, and config

There is no config file format in the current code. Configuration comes from environment
variables plus CLI flags.

| CLI flag | Environment | Default | Meaning |
|---|---|---|---|
| `--listen` | `LLMTESTLAB_LISTEN` | `:7777` | Proxy listen address. |
| `--control` | `LLMTESTLAB_CONTROL_LISTEN` | `127.0.0.1:7778` | Control API listen address. |
| `--control-token` | `LLMTESTLAB_CONTROL_TOKEN` | empty | Bearer token required by the control API when set. Also required when the control listener is non-loopback. |
| `--upstream` | `LLMTESTLAB_UPSTREAMS`, fallback `LLMTESTLAB_UPSTREAM` | none | Upstream mapping. Repeat the flag or use comma-separated environment values. |
| `--tls` | none | `false` | Parsed by the CLI, but not wired to a TLS listener in the current code. |

At least one upstream is required.

```bash
llmtestlab \
  --listen 127.0.0.1:7777 \
  --control 127.0.0.1:7778 \
  --upstream openai=https://api.openai.com/v1 \
  --upstream azure=https://my-resource.openai.azure.com
```

Equivalent environment configuration:

```bash
export LLMTESTLAB_LISTEN=127.0.0.1:7777
export LLMTESTLAB_CONTROL_LISTEN=127.0.0.1:7778
export LLMTESTLAB_UPSTREAMS='openai=https://api.openai.com/v1,azure=https://my-resource.openai.azure.com'
llmtestlab
```

`--upstream` accepts either `name=https://host/base` or a bare URL. A bare URL is stored
under the name `default`. If the same name is configured more than once, the later value
wins.

## Named upstreams and path prefixes

Named upstreams are addressed with `/u/{name}/...` on the proxy listener. The rest of the
path is joined to the configured upstream base path.

```text
http://127.0.0.1:7777/u/openai/chat/completions
  -> https://api.openai.com/v1/chat/completions

http://127.0.0.1:7777/u/azure/openai/deployments/my-deployment/chat/completions?api-version=2024-10-21
  -> https://my-resource.openai.azure.com/openai/deployments/my-deployment/chat/completions?api-version=2024-10-21
```

Bare proxy paths are accepted only when exactly one upstream exists or when an upstream
named `default` exists:

```text
http://127.0.0.1:7777/v1/chat/completions
```

With multiple upstreams and no `default`, a bare path returns `502 Bad Gateway` with
`bare proxy path requires exactly one upstream or a default upstream`.

## API type detection

The parser chooses an API family from the upstream method and final upstream path:

| Condition | `api_family` |
|---|---|
| `POST` and path ends with `/chat/completions` | `openai.chat` |
| `POST` and path ends with `/responses` | `openai.responses` |
| Anything else | `passthrough` |

Passthrough calls are still recorded, including headers, raw request/response bytes,
status, timings, and spans. They are not parsed into tool calls, usage, or provider
finish reasons.

Azure `api-version` is extracted from the upstream query string. Azure deployment is
extracted from `/deployments/{deployment}/...` path segments.

## Session tagging

Each call is stored in one session:

1. `X-Test-Session: <id>` wins.
2. If the header is absent, W3C `baggage` is scanned for one of:
   - `llmtestlab.session.id`
   - `llmtestlab.session`
   - `test-session`
   - `x-test-session`
3. If no tag is present, the call lands in the `default` session.

The Go client's `llmtestlab.New(t)` creates a random session id and can inject
`X-Test-Session` through `lab.HTTPClient()`.

## Header forwarding and stripping

Before forwarding upstream, the proxy:

- removes `X-Test-Session`;
- removes any `baggage` member whose key starts with `llmtestlab.`;
- preserves non-`llmtestlab.*` baggage members;
- injects W3C trace context for the proxy CLIENT span;
- sets `Accept-Encoding: identity`;
- removes hop-by-hop headers:
  `Connection`, `Proxy-Connection`, `Keep-Alive`, `Proxy-Authenticate`,
  `Proxy-Authorization`, `Te`, `Trailer`, `Transfer-Encoding`, and `Upgrade`.

Auth headers are forwarded to the provider. They are only redacted in the recorded copy.

## SSE passthrough semantics

Streaming responses are passed through byte-for-byte to the SDK client. The proxy flushes
after each copied chunk when the response writer supports flushing.

For recording:

- `Content-Type: text/event-stream` is parsed as SSE.
- If the SSE response is not gzip-encoded, events are recorded incrementally as chunks
  arrive.
- If the upstream still returns gzip, the client receives the gzip bytes unchanged, while
  the recorded tee copy is decompressed for parsing.
- A Chat Completions stream is considered complete when a `data: [DONE]` event is seen.
- A Responses stream is considered complete when a terminal `response.completed` event is
  parsed.
- SSE comment lines, including OpenRouter keep-alive comments, are recorded as comments
  and ignored by the normalizer.
- If no terminal marker/event is seen, `normalized.stream_truncated` is set.

## Secret redaction

Recorded headers redact these names by replacing values with `sha256:<16 hex chars>`:

- `Authorization`
- `api-key`
- `x-api-key`
- `x-goog-api-key`
- `Cookie`
- `Set-Cookie`

Recorded upstream URLs redact these query parameters the same way:

- `key`
- `api_key`
- `sig`
- `signature`

Raw request and response bodies are stored for test assertions and are not redacted by the
current implementation. Treat the local control API as sensitive.

## Control API bind and token security

The default control address is loopback-only: `127.0.0.1:7778`.

If `--control-token` or `LLMTESTLAB_CONTROL_TOKEN` is set, every control API request must
send:

```http
Authorization: Bearer <token>
```

If the control address is non-loopback, validation fails unless a control token is set.
`localhost` and loopback IP addresses are accepted as loopback. A hostless bind such as
`:7778` is treated as non-loopback and therefore requires a token.

## Control endpoints

All endpoints are on the control listener, not the proxy listener.

### `GET /healthz`

Returns `200 OK` and a plain text body.

```bash
curl -i http://127.0.0.1:7778/healthz
```

```http
HTTP/1.1 200 OK

ok
```

### `GET /sessions/{id}`

Returns the current session immediately.

```bash
curl http://127.0.0.1:7778/sessions/test-1
```

```json
{
  "id": "test-1",
  "calls": [
    {
      "id": "call_1",
      "session_id": "test-1",
      "session_source": "header",
      "seq": 1,
      "started_at": "2026-10-18T08:54:00Z",
      "ended_at": "2026-10-18T08:54:01Z",
      "latency_ms": 742,
      "provider": "openai",
      "api_family": "openai.chat",
      "upstream": {
        "method": "POST",
        "url": "https://api.openai.com/v1/chat/completions"
      },
      "request": {
        "headers": {
          "Authorization": ["sha256:0123456789abcdef"]
        }
      },
      "response": {
        "headers": {
          "Content-Type": ["application/json"]
        }
      },
      "stream": false,
      "http_status": 200,
      "normalized": {
        "derived_version": 1,
        "model": {
          "requested": "gpt-4.1-mini",
          "returned": "gpt-4.1-mini"
        },
        "finish_reason": "stop"
      }
    }
  ]
}
```

`raw_request`, `raw_response`, and `sse_events[].data` are Go `[]byte` fields and appear
as base64 strings in JSON.

### `GET /sessions/{id}/wait`

Waits until the session has no active calls and stays unchanged for a quiet period.

Query parameters:

| Name | Default | Meaning |
|---|---|---|
| `quiet` | `500ms` | Required quiet period after the last version change. Parsed by Go `time.ParseDuration`. |
| `timeout` | `30s` | Maximum wait before returning `504 Gateway Timeout`. Parsed by Go `time.ParseDuration`. |

```bash
curl 'http://127.0.0.1:7778/sessions/test-1/wait?quiet=100ms&timeout=5s'
```

Response body is the same session JSON shape as `GET /sessions/{id}`.

### `GET /sessions/{id}/otlp`

Returns OTLP/JSON trace data for spans recorded in the session.

```bash
curl http://127.0.0.1:7778/sessions/test-1/otlp
```

```json
{
  "resourceSpans": [
    {
      "resource": {
        "attributes": [
          {
            "key": "service.name",
            "value": { "stringValue": "llmtestlab-proxy" }
          }
        ]
      },
      "scopeSpans": [
        {
          "scope": {
            "name": "github.com/barzoj-co/llm-test-lab/proxy",
            "version": "0.1.0-dev"
          },
          "spans": []
        }
      ]
    }
  ]
}
```

See [OTel GenAI mapping](otel.md) for span attributes.

### `DELETE /sessions/{id}`

Deletes the session calls and OTLP spans.

```bash
curl -i -X DELETE http://127.0.0.1:7778/sessions/test-1
```

```http
HTTP/1.1 204 No Content
```

### Unsupported methods

Unsupported methods under `/sessions/{id}` return `405 Method Not Allowed` with:

```http
Allow: GET, DELETE
```
