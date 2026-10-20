# OTel GenAI mapping

`llmentary` emits one OTLP/JSON CLIENT span for each recorded upstream proxy call.
The span is modeled first as a normal REST/HTTP client call using the stable OTel HTTP
client semantic conventions. GenAI attributes from
`open-telemetry/semantic-conventions-genai` commit `f49da22` are layered on that same span
as additional attributes. The pinned GenAI attribute names are defined in
`internal/semconv/genai/attributes.go`.

The local in-memory span store captures content by default because this project is a local
test tool. There is no external OTLP exporter in the current code. The existing
normalized session JSON remains available at `GET /sessions/{id}`. Spans are exposed
separately as OTLP/JSON at:

```text
GET /sessions/{id}/otlp
```

Trace and span IDs are hex strings in the OTLP/JSON response.

Implementation uses the official `go.opentelemetry.io/otel` SDK (`sdk/trace`) with
`sdktrace.AlwaysSample()` and a custom in-memory `SpanProcessor`. The `/otlp` endpoint is
encoded from `go.opentelemetry.io/proto/otlp` trace protos via `protojson`, with the
trace/span ID fields normalized to OTLP/JSON hex strings.

## Span shape

- Span name: `chat {request model}`. If no request model is parsed, Azure deployment is
  used as a fallback; otherwise the model is `unknown`.
- Span kind: `SPAN_KIND_CLIENT`.
- Stable HTTP client attributes: `http.request.method`, redacted `url.full`,
  `server.address`, `server.port`, `http.response.status_code`,
  `http.request.body.size`, `http.response.body.size`, `error.type`, and
  `http.request.resend_count` when present.
- Span status: standard HTTP client span status rules, plus provider/mid-stream errors
  detected in otherwise-HTTP-200 LLM streams.
- Parent: incoming W3C `traceparent`, if present.
- Sampling: always record spans, including when the incoming `traceparent` has sampled flag
  `00`.
- Outbound propagation: default `--propagate-trace=off` forwards the client's
  `traceparent` unchanged and injects none. `--propagate-trace=auto` injects a
  `traceparent` for the proxy CLIENT span into the upstream request.
- Session:
  - `--session-from` default `header,baggage,path,default`.
  - `X-Test-Session` wins (when `header` is enabled) and is stripped before forwarding.
  - Otherwise baggage key `llmentary.session.id` is used (when `baggage` is enabled).
  - `/s/{session}/` is a path source (when `path` is enabled) and is stripped before forwarding.
  - All `llmentary.*` baggage members are stripped before forwarding; other baggage is
    preserved.

## Common attributes

Every proxied call sets the stable HTTP client attributes plus common GenAI/custom
attributes where values are available. OpenAI-specific attributes are set only for parsed
Chat Completions or Responses calls.

| Attribute | Source |
|---|---|
| `gen_ai.operation.name` | constant `chat` |
| `gen_ai.provider.name` | known upstream host mapping or route label |
| `gen_ai.request.model` | request `model`, Azure deployment path fallback, or `unknown` |
| `server.address`, `server.port` | real upstream host/port |
| `http.request.method` | upstream request method |
| `url.full` | redacted upstream URL |
| `http.response.status_code` | upstream HTTP status |
| `http.request.resend_count` | `x-stainless-retry-count`, when `> 0` |
| `http.request.body.size`, `http.response.body.size` | recorded byte lengths |
| `openai.api.type` | `chat_completions` or `responses` for OpenAI-wire parsers |
| `gen_ai.response.id` | response/body id when present |
| `gen_ai.response.model` | returned model when present |
| `gen_ai.response.finish_reasons` | Responses normalized finish reason, otherwise native finish reason when present, otherwise normalized finish reason |
| `gen_ai.usage.*` | normalized upstream usage when present |
| `gen_ai.response.time_to_first_chunk` | streaming TTFT in seconds |
| `gen_ai.input.messages` | structured captured input messages |
| `gen_ai.output.messages` | structured captured assistant output/tool calls |
| `gen_ai.tool.definitions` | type/name only |
| `llmentary.*` | test/session/parser metadata |

Selected request/response headers are also copied to span attributes with
`http.request.header.` or `http.response.header.` prefixes:

- `x-request-id`
- `apim-request-id`
- `retry-after`
- `retry-after-ms`
- `openai-processing-ms`
- any `x-stainless-*` header

## Chat Completions specifics

- `gen_ai.input.messages` is derived from `messages[]`.
- System and developer messages remain in `gen_ai.input.messages`.
- Tool results sent back in a request are mapped as `tool_call_response` parts.
- Tool calls requested by the model are mapped in `gen_ai.output.messages` as `tool_call`
  parts with best-effort JSON arguments.
- Request attributes include `temperature`, `top_p`, penalties, `seed`, `n`, max tokens,
  stop sequences, `reasoning_effort`, service tier, stream, and output type when present.

## Responses specifics

- `instructions` is emitted as `gen_ai.system_instructions`.
- `input` string is mapped as one user text message.
- `input[]` items are mapped as structured input messages/tool responses when recognized.
- `output[]` function calls become `tool_call` parts in one assistant output message.
- `previous_response_id`, `conversation`, `reasoning.effort`, `max_output_tokens`,
  service tier, stream, and output type are mapped when present.

## `llmentary.*` attributes

| Attribute | Meaning |
|---|---|
| `llmentary.session.id` | test session id |
| `llmentary.session.source` | `header`, `baggage`, or `default` |
| `llmentary.call.id` | proxy call id |
| `llmentary.call.seq` | order in session |
| `llmentary.prompt.chars` | normalized prompt character count |
| `llmentary.prompt.message_count` | message/input item count |
| `llmentary.system_prompt.hash` | SHA-256 of system/developer/instructions text |
| `llmentary.tool_definitions.hash` | SHA-256 of declared tools |
| `llmentary.tool_calls.names` | output tool names in order |
| `llmentary.finish_reasons.normalized` | cross-API finish reason |
| `llmentary.stream.chunk_count` | raw SSE event count |
| `llmentary.stream.truncated` | stream ended without terminal marker |
| `llmentary.client.aborted` | client copy failed/aborted |
| `llmentary.error.mid_stream` | error appeared inside an HTTP-200 stream |
| `llmentary.azure.api_version` | Azure `api-version` query value |
| `llmentary.azure.deployment` | Azure deployment path segment |
| `llmentary.parser.warnings` | parser warnings |
| `llmentary.derived.version` | normalized schema version |

## Deliberate deviations

1. **One span per retry attempt.** GenAI describes one inference span covering SDK retries
   from the caller's point of view. The proxy sits behind the SDK, so each retry is a real
   inbound HTTP request and a separately recorded attempt. We emit one CLIENT span per
   attempt and set `http.request.resend_count` from `x-stainless-retry-count`.

2. **Content capture is on in the local store.** GenAI says instrumentations should not
   capture prompts/outputs by default. This tool is explicitly for local deterministic
   testing, so the in-memory store captures structured `gen_ai.input.messages`,
   `gen_ai.output.messages`, `gen_ai.system_instructions`, and `gen_ai.tool.definitions`.
   Future external OTLP export should default content off.
