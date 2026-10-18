# OTel GenAI mapping

`llm-test-lab` emits one OTLP/JSON CLIENT span for each recorded upstream proxy call.
The span is modeled first as a normal REST/HTTP client call using the stable OTel HTTP
client semantic conventions. GenAI attributes from
`open-telemetry/semantic-conventions-genai` commit `f49da22` are layered on that same span
as additional attributes.

The local in-memory span store captures content by default because this project is a local
test tool. The existing normalized session JSON remains available at `GET /sessions/{id}`.
Spans are exposed separately as OTLP/JSON at:

```text
GET /sessions/{id}/otlp
```

Trace and span IDs are hex strings in the OTLP/JSON response.

Implementation uses the official `go.opentelemetry.io/otel` SDK (`sdk/trace`) with
`sdktrace.AlwaysSample()` and a custom in-memory `SpanProcessor`. The `/otlp` endpoint is
encoded from `go.opentelemetry.io/proto/otlp` trace protos via `protojson`, with the
trace/span ID fields normalized to OTLP/JSON hex strings.

## Span shape

- Span name: `chat {gen_ai.request.model}` for parsed LLM calls.
- Span kind: `SPAN_KIND_CLIENT`.
- Stable HTTP client attributes: `http.request.method`, redacted `url.full`,
  `server.address`, `server.port`, `http.response.status_code`, `error.type`,
  and `http.request.resend_count` when present.
- Span status: standard HTTP client span status rules, plus provider/mid-stream errors
  detected in otherwise-HTTP-200 LLM streams.
- Parent: incoming W3C `traceparent`, if present.
- Sampling: always record spans, including when the incoming `traceparent` has sampled flag
  `00`.
- Outbound propagation: the proxy injects a `traceparent` for its CLIENT span into the
  upstream request.
- Session:
  - `X-Test-Session` wins and is stripped before forwarding.
  - Otherwise baggage key `llmtestlab.session.id` is used.
  - All `llmtestlab.*` baggage members are stripped before forwarding; other baggage is
    preserved.

## Common attributes

Every parsed Chat Completions or Responses call sets the stable HTTP client attributes
plus the following GenAI/custom attributes:

| Attribute | Source |
|---|---|
| `gen_ai.operation.name` | constant `chat` |
| `gen_ai.provider.name` | upstream host or route label |
| `gen_ai.request.model` | request `model`, or Azure deployment path fallback |
| `server.address`, `server.port` | real upstream host/port |
| `http.request.method` | upstream request method |
| `url.full` | redacted upstream URL |
| `http.response.status_code` | upstream HTTP status |
| `http.request.resend_count` | `x-stainless-retry-count`, when `> 0` |
| `http.request.body.size`, `http.response.body.size` | recorded byte lengths |
| `openai.api.type` | `chat_completions` or `responses` for OpenAI-wire parsers |
| `gen_ai.response.id` | response/body id when present |
| `gen_ai.response.model` | returned model when present |
| `gen_ai.response.finish_reasons` | raw/native finish reason |
| `gen_ai.usage.*` | normalized upstream usage when present |
| `gen_ai.response.time_to_first_chunk` | streaming TTFT in seconds |
| `gen_ai.input.messages` | structured captured input messages |
| `gen_ai.output.messages` | structured captured assistant output/tool calls |
| `gen_ai.tool.definitions` | type/name only |
| `llmtestlab.*` | test/session/parser metadata |

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

## `llmtestlab.*` attributes

| Attribute | Meaning |
|---|---|
| `llmtestlab.session.id` | test session id |
| `llmtestlab.session.source` | `header`, `baggage`, or `default` |
| `llmtestlab.call.id` | proxy call id |
| `llmtestlab.call.seq` | order in session |
| `llmtestlab.prompt.chars` | normalized prompt character count |
| `llmtestlab.prompt.message_count` | message/input item count |
| `llmtestlab.system_prompt.hash` | SHA-256 of system/developer/instructions text |
| `llmtestlab.tool_definitions.hash` | SHA-256 of declared tools |
| `llmtestlab.tool_calls.names` | output tool names in order |
| `llmtestlab.finish_reasons.normalized` | cross-API finish reason |
| `llmtestlab.stream.chunk_count` | raw SSE event count |
| `llmtestlab.stream.truncated` | stream ended without terminal marker |
| `llmtestlab.client.aborted` | client copy failed/aborted |
| `llmtestlab.error.mid_stream` | error appeared inside an HTTP-200 stream |
| `llmtestlab.azure.api_version` | Azure `api-version` query value |
| `llmtestlab.azure.deployment` | Azure deployment path segment |
| `llmtestlab.parser.warnings` | parser warnings |
| `llmtestlab.derived.version` | normalized schema version |

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
