# Recorded trace data model

The control API returns one `Session` per test id. A session contains ordered `Call`
records. The Go structs live in `internal/proxy/types.go`; this document describes their
JSON shape and normalization rules.

## Session

```json
{
  "id": "session-id",
  "calls": []
}
```

| Field | Type | Meaning |
|---|---|---|
| `id` | string | Session id from `X-Test-Session`, baggage, `/s/{session}` path, or `default`. |
| `calls` | array of `Call` | Calls recorded for the session in sequence order. |

## Call

```json
{
  "id": "call_1",
  "session_id": "session-id",
  "session_source": "header",
  "seq": 1,
  "started_at": "2026-10-18T08:54:00Z",
  "first_byte_at": "2026-10-18T08:54:00.120Z",
  "ended_at": "2026-10-18T08:54:00.742Z",
  "latency_ms": 742,
  "provider": "openai",
  "api_family": "openai.chat",
  "upstream": {
    "method": "POST",
    "url": "https://api.openai.com/v1/chat/completions",
    "api_version": "",
    "deployment": ""
  },
  "request": {
    "headers": {}
  },
  "response": {
    "headers": {}
  },
  "request_ids": {
    "x-request-id": "req_..."
  },
  "raw_request": "eyJtb2RlbCI6ImdwdC00LjEtbWluaSJ9",
  "raw_response": "eyJpZCI6ImNoYXRjbXBsXy4uLiJ9",
  "sse_events": [],
  "stream": false,
  "http_status": 200,
  "retry_count": 0,
  "retry_after": "",
  "normalized": {}
}
```

| Field | Type | Meaning |
|---|---|---|
| `id` | string | Proxy-generated id, currently `call_` plus base-36 counter. Unique within one proxy process, shared counter across sessions. Do not use it for order; use `seq`. |
| `session_id` | string | Session bucket. |
| `session_source` | string | `header`, `baggage`, `path`, or `default`. |
| `seq` | number | 1-based order within the session. |
| `started_at` | timestamp | UTC time before the upstream request starts. |
| `first_byte_at` | timestamp, optional | UTC time when the first upstream response byte is copied to the client. |
| `ended_at` | timestamp | UTC time after the upstream response copy completes or a proxy error is recorded. |
| `latency_ms` | number | `ended_at - started_at` in milliseconds. |
| `provider` | string | Upstream route name. |
| `api_family` | string | `openai.chat`, `openai.responses`, or `passthrough`. |
| `upstream` | object | Method, redacted URL, Azure API version, and Azure deployment when available. |
| `request.headers` | object | Recorded request headers with secret headers hashed. |
| `response.headers` | object | Recorded response headers with secret headers hashed. |
| `request_ids` | object | Response IDs from selected headers/body fields. |
| `raw_request` | base64 string | Raw request body bytes. |
| `raw_response` | base64 string | Raw response bytes used for parsing. **Gzip responses are stored decompressed** (see below). |
| `sse_events` | array | Parsed SSE events for streaming responses. |
| `stream` | boolean | Whether the response was detected as SSE. When the client aborts before response headers, this is the request body's `stream` flag. |
| `http_status` | number | Upstream HTTP status, or proxy error status. `0` when the client cancelled before response headers (never a synthetic `502`). |
| `retry_count` | number, optional | Integer parsed from `x-stainless-retry-count`. |
| `retry_after` | string, optional | `retry-after-ms` or `retry-after` response header. |
| `normalized` | object | Cross-provider view used by test assertions. |

### Gzip and raw-byte hashes (E6)

`raw_request` is the exact bytes the proxy forwarded upstream. `raw_response` is the
bytes used for parsing, **not** always the bytes delivered to the client:

- The client always receives the upstream envelope unchanged, including
  `Content-Encoding: gzip`.
- When the upstream body is gzip, deflate, brotli (`br`), or zstd, the recorded
  `raw_response` is the **decompressed** payload. Parsing (and therefore
  normalization) cannot run on the compressed envelope, and a later test that
  hashes `raw_response` would otherwise fail to match the JSON the provider
  actually produced. Unknown `Content-Encoding` values stay raw and a WARN is logged.

`TestE6GoldenRawHashes` therefore asserts `sha256(raw_request) == sha256(upstream request body)`
and `sha256(raw_response) == sha256(decompressed upstream body)` for every fixture,
including the gzip case. `TestE6GzipRawBytes` additionally checks that the client
body still hashes to the gzip envelope.

## Upstream

```json
{
  "method": "POST",
  "url": "https://api.openai.com/v1/chat/completions?key=sha256:0123456789abcdef",
  "api_version": "2024-10-21",
  "deployment": "my-deployment"
}
```

`api_version` is copied from the `api-version` query parameter. `deployment` is copied
from an Azure-style `/deployments/{deployment}/...` path segment.

## SSEEvent

```json
{
  "event": "response.completed",
  "data": "eyJ0eXBlIjoicmVzcG9uc2UuY29tcGxldGVkIn0=",
  "comment": "",
  "at_ms": 221
}
```

`data` is base64 because it is a Go `[]byte`. Comment events have `comment` set and no
`data`. `at_ms` is elapsed time from `started_at` to the parser recording the event.

## Normalized

```json
{
  "derived_version": 3,
  "model": {
    "requested": "gpt-4.1-mini",
    "returned": "gpt-4.1-mini"
  },
  "served_by": "openai",
  "system_prompt_hash": "7a38...",
  "system_prompt": "You are concise.",
  "messages_count": 2,
  "prompt_chars": 96,
  "context_server_side": false,
  "previous_response_id": "",
  "tools_declared": [],
  "tool_calls": [],
  "tool_results_in": [],
  "output_text": "done",
  "refusal": "",
  "reasoning": {
    "present": true,
    "chars": 128
  },
  "finish_reason": "stop",
  "native_finish_reason": "completed",
  "usage": {},
  "content_filter": {},
  "error": null,
  "stream_truncated": false,
  "client_aborted": false,
  "parser_warnings": [],
  "raw": null
}
```

| Field | Meaning |
|---|---|
| `derived_version` | Normalized schema version. Current value is `3`. Version 3 adds `validation`, `spec_violations`, and `ignored_fields` (additive). |
| `model.requested` | Request `model`, or empty if not parsed. |
| `model.returned` | Response model when present. |
| `served_by` | Provider field from compatible responses when present. |
| `system_prompt_hash` | SHA-256 of normalized system/developer/instructions text. |
| `system_prompt` | Captured system/developer/instructions text. |
| `messages_count` | Chat `messages[]` count or Responses input item count. |
| `prompt_chars` | Request size by default, or parsed prompt text character count for OTel. |
| `context_server_side` | Responses requests with `previous_response_id` or `conversation`. |
| `previous_response_id` | Responses request field. |
| `tools_declared` | Declared tools with name, type, and schema hash. |
| `tool_calls` | Tool calls emitted by the model. |
| `tool_results_in` | Tool results included in the request. |
| `output_text` | Concatenated assistant text output. |
| `refusal` | Chat `message.refusal` / stream `delta.refusal`, or a Responses `refusal` content part. `finish_reason` is preserved separately. |
| `reasoning` | Presence/size of exposed reasoning fields. The proxy does not decrypt hidden reasoning. |
| `finish_reason` | Cross-API normalized finish reason. |
| `native_finish_reason` | Provider status/finish field when distinct. |
| `usage` | Normalized token/cost usage. |
| `content_filter` | Provider content-filter data when parsed. |
| `error` | Normalized provider/proxy error. |
| `stream_truncated` | Stream ended without a terminal marker/event. |
| `client_aborted` | The client disconnected before the terminal stream event (`[DONE]`, or Responses `response.completed` / `response.failed` / `response.incomplete`), or cancelled the inbound request before response headers arrived. A close after a terminal event is not an abort. |
| `parser_warnings` | Non-fatal parser warnings. |
| `validation` | Present in `warn`/`enforce`. Mode, profile, spec SHA, subset retrieval date, checked flags, optional `skipped_reason`, `duration_us`, and `truncated`. Absent in `off`. |
| `spec_violations` | Pointer-to-slice so `[]` (clean) is distinct from absent (not checked). Sorted by side, pointer, keyword. Capped at 200. |
| `ignored_fields` | Silently ignored request content (unknown schema keywords, unknown request fields, strict fallback). Same empty-vs-absent rule as `spec_violations`. |
| `raw` | Reserved raw normalized payload field; currently not populated by parsers. |

### Spec validation fields

```json
"validation": {
  "mode": "warn", "profile": "relaxed",
  "spec_sha": "<openai-openapi commit>", "schema_subset_retrieved": "2026-10-18",
  "request_checked": true, "response_checked": true,
  "skipped_reason": null,
  "duration_us": 412
}
```

`spec_violations` entries use `side` (`request`/`response`), `source` (`syntax`/`schema`/`strict_mode`), `path`, `pointer`, `keyword`, `message`, `profile`, and optional `offset` / `event_index` / `count`. Identical rows (same side, keyword, pointer, message) are stored once with `count=N`; streamed copies keep the first `event_index`. `unevaluatedProperties` / `additionalProperties` messages name the unknown field.

`ignored_fields` entries use `path`, `pointer`, `key`, `kind`, `context`, `effective_strict`, `upstream_behavior`, `evidence`, `severity`, `action`, `hint`, optional `suggested_description`, and `confirmed`.

In `off` mode the three keys are missing. In `warn`/`enforce` the two arrays are always present.

Where findings show up: trace (`normalized.spec_violations`), proxy log (WARN), client assertions.

| Field | What it is | Empty vs absent |
|---|---|---|
| `validation` | Metadata: was the call checked, and how (`mode`, `profile`, `spec_sha`, checked flags, `skipped_reason`). Not a list of problems. | Absent when validation was not run (`off`). Present in `warn`/`enforce` even when the call is clean. |
| `spec_violations` | Findings from the spec validator. | `[]` means checked and clean. Absent means not checked. |
| `ignored_fields` | Silently ignored request content (unknown schema keywords, unknown request fields, strict fallback). | Same empty-vs-absent rule as `spec_violations`. |

## Tools

Declared tool:

```json
{
  "name": "get_weather",
  "type": "function",
  "schema_hash": "..."
}
```

Tool call:

```json
{
  "index": 0,
  "id": "call_abc",
  "item_id": "fc_123",
  "name": "get_weather",
  "namespace": "",
  "kind": "function",
  "arguments_raw": "{\"city\":\"Paris\"}",
  "arguments": { "city": "Paris" },
  "args_parse_error": ""
}
```

Tool result:

```json
{
  "call_id": "call_abc",
  "name": "get_weather",
  "output_hash": "..."
}
```

For Chat Completions, tool results come from `role: "tool"` messages. For Responses,
they come from `type: "function_call_output"` input items.

## Finish reasons

Chat Completions uses provider `finish_reason` when present. Some compatible providers
also expose `native_finish_reason`.

Responses synthesizes `finish_reason` as follows:

| Responses status/details | `finish_reason` |
|---|---|
| output contains one or more tool calls | `tool_calls` |
| `status: "completed"` | `stop` |
| `status: "failed"` or `status: "cancelled"` | `error` |
| `status: "incomplete"` and reason `content_filter` | `content_filter` |
| `status: "incomplete"` and reason `max_output_tokens` | `length` |
| other `status: "incomplete"` | `incomplete` |
| other non-empty status | the status string |
| no status and no parser result | `unknown` |

Proxy network errors set `finish_reason: "error"` with `error.normalized: "network"`
and an HTTP `502`. A client cancel before response headers sets
`finish_reason: "cancelled"`, `client_aborted: true`, `http_status: 0`,
`error.normalized: "client_aborted"`, and `stream` from the request body. The
parser is not run on that empty body, so there is no "strict decode failed"
warning. `upstream_called` is true when the outbound request was written
(`httptrace.WroteRequest`). Passthrough calls without errors use
`finish_reason: "unknown"`.

## Errors inside HTTP 200 streams

LLM APIs can report errors inside an otherwise successful HTTP stream. The parsers look
for JSON `error` fields in stream chunks and set:

```json
{
  "http_status": 200,
  "type": "too_many_requests",
  "code": "no_capacity",
  "message": "try later",
  "normalized": "unknown",
  "mid_stream": true
}
```

HTTP status errors (`>=400`) are also normalized from response body `error` objects when
possible, with `mid_stream: false`. `mid_stream` is true when the error arrived in an
HTTP 200 JSON response or after an HTTP 200 stream had started (an SSE error event or
error chunk). Network/proxy errors are recorded with a proxy HTTP status and
`normalized: "network"`.

Error normalization categories are:

- `auth`
- `rate_limit`
- `server`
- `content_filter`
- `bad_request`
- `network`
- `unknown`

## Usage

Chat Completions usage maps:

| Provider field | Normalized field |
|---|---|
| `prompt_tokens` | `input_tokens` |
| `completion_tokens` | `output_tokens` |
| `total_tokens` | `total_tokens` |
| `prompt_tokens_details.cached_tokens` | `cached_input_tokens` |
| `prompt_tokens_details.cache_write_tokens` | `cache_write_tokens` |
| `prompt_tokens_details.audio_tokens` | `audio_tokens` |
| `completion_tokens_details.reasoning_tokens` | `reasoning_tokens` |
| `completion_tokens_details.audio_tokens` | `audio_tokens` (when prompt audio is absent) |
| `completion_tokens_details.accepted_prediction_tokens` | `accepted_prediction_tokens` |
| `completion_tokens_details.rejected_prediction_tokens` | `rejected_prediction_tokens` |
| `cost` | `cost_usd` |

Responses usage maps:

| Provider field | Normalized field |
|---|---|
| `input_tokens` | `input_tokens` |
| `output_tokens` | `output_tokens` |
| `total_tokens` | `total_tokens` |
| `input_tokens_details.cached_tokens` | `cached_input_tokens` |
| `output_tokens_details.reasoning_tokens` | `reasoning_tokens` |

`usage.source` is `body`, `final_chunk`, or `terminal_event` depending on where usage was
found.

## Timing and TTFT

`latency_ms` is measured around the full upstream request/response lifecycle seen by the
proxy. For streams, it includes the time until the stream copy finishes.

`first_byte_at` is set when the proxy copies the first response byte to the client. TTFT
is:

```text
first_byte_at - started_at
```

Clients can assert TTFT from `first_byte_at - started_at`. The Go client lives in
[llmentary-go](https://github.com/barzoj-co/llmentary-go).

## Retries

Each SDK retry appears as a separate `Call` because the proxy sees a separate HTTP
request. `retry_count` is parsed from request header `x-stainless-retry-count` when that
header is present and integer-valued.

Clients can count or exclude retries (`retry_count` present and greater than zero).
The Go client lives in [llmentary-go](https://github.com/barzoj-co/llmentary-go).
