# Session lifecycle

How `GET /sessions/{id}`, `GET /sessions/{id}/wait`, `DELETE /sessions/{id}`,
and `GET /sessions` treat a session id.

| State | `GET /sessions/{id}` | `wait` `min_calls=0` | `wait` `min_calls>=1` | `DELETE` | `GET /sessions` list |
|---|---|---|---|---|---|
| Never created | `404` `session_not_found` | `404` `session_not_found` immediately | wait until `timeout`, then `504` `wait_timeout` | `204` (idempotent) | absent |
| In-flight only (0 settled) | `200` empty `calls` | wait while `active>0` | wait while `active>0` | `204` | present, `active>0`, `calls=0` |
| Has settled calls | `200` session (settled `calls` only) | `200` after quiet | `200` when enough settled calls are quiet | `204` | present |
| Deleted | `404` `session_not_found` | `404` `session_not_found` immediately | wait until `timeout`, then `504` `wait_timeout` | `204` (idempotent) | absent |

A never-created session is never a `200` with an empty `calls` array. An
in-flight first call is: the session exists (`200`) but `calls` stays empty
until the final record is published. Use `min_calls>=1` when an async worker
may still create the session. `wait` counts only settled calls (`ended_at`
set); `GET /sessions/{id}` and a successful `wait` never return an abort
placeholder (`http_status=0`, `client_aborted=false`).

A `504` `wait_timeout` still returns those settled `calls`, and the error
object adds `active` plus `in_flight` (never inside `calls`) so a stalled
upstream is visible. Each `in_flight` item has `call_id`, `seq`, `method`,
`path`, `api_family`, `upstream`, `started_at`, `elapsed_ms`, `phase`
(`awaiting_headers` or `streaming`), `bytes_received`, and `http_status`
once headers have arrived. No bodies or headers. Pin the shape from
[`api/control/testdata/wait_timeout_inflight.json`](../api/control/testdata/wait_timeout_inflight.json).
`GET /sessions/{id}?include=in_flight` returns the same `in_flight` array
next to `calls`.

Machine-readable contract: [`api/control/openapi.yaml`](../api/control/openapi.yaml).
Control API reference: [Proxy](proxy.md#get-sessionsidwait).

`GET /sessions[].spec` counts how each finished call was validated. Canonical
keys are snake_case (`checked`, `clean`, `findings`, `request_only`,
`not_checked`); PascalCase aliases are still serialized and marked
deprecated in OpenAPI (removed in 1.0). See
[Proxy — GET /sessions](proxy.md#get-sessions-and-get-sessions).

## Abort cases

| When the client disconnects | `client_aborted` | log `phase` | `http_status` | `finish_reason` | Partial body |
|---|---|---|---|---|---|
| Before upstream response headers | `true` | `before_headers` | `0` (never `502`) | `cancelled` | none |
| After headers / mid-SSE (no terminal event yet) | `true` | `after_headers` | upstream status, usually `200` | `cancelled` | deltas received so far |
| After `[DONE]` / `response.completed` / `failed` / `incomplete` | `false` | omitted | `200` | parser value (e.g. `stop`) | full stream |

`upstream_called` is true when the outbound request was written
(`httptrace.WroteRequest`). After-headers aborts are not a `network` error
and do not set `error.mid_stream` (that flag is for HTTP/SSE *errors* after
a stream started). The abort `proxy response` line always includes `seq=`
and `spec=` (`request_only` when the response was not validated).

Abort JSON (top-level fields; `normalized.client_aborted` is a deprecated alias):

```json
{
  "id": "call_1",
  "session_id": "barge",
  "seq": 1,
  "http_status": 0,
  "client_aborted": true,
  "abort_phase": "before_headers",
  "normalized": {
    "finish_reason": "cancelled",
    "client_aborted": true,
    "error": {"normalized": "client_aborted"}
  }
}
```

## Example: `wait` on a missing session

```bash
# no calls will ever arrive; do not wait for quiet
curl -i 'http://127.0.0.1:7778/sessions/ghost/wait?timeout=3s&quiet=200ms&min_calls=0'
```

```http
HTTP/1.1 404 Not Found
Content-Type: application/json
```

```json
{
  "id": "ghost",
  "error": {
    "type": "session_not_found",
    "message": "no calls recorded for session \"ghost\"; list sessions with GET /sessions"
  },
  "waited_ms": 1
}
```

```bash
# a worker may still start a call; wait until timeout if it does not
curl -i 'http://127.0.0.1:7778/sessions/ghost/wait?timeout=1s&quiet=50ms&min_calls=1'
```

```http
HTTP/1.1 504 Gateway Timeout
Content-Type: application/json
```

```json
{
  "id": "ghost",
  "calls": [],
  "error": {
    "type": "wait_timeout",
    "message": "wait timed out",
    "calls_seen": 0,
    "min_calls": 1,
    "active": 0,
    "in_flight": []
  }
}
```

A wait that times out while a call is still running (headers never arrived):

```json
{
  "id": "stall-example",
  "calls": [],
  "error": {
    "type": "wait_timeout",
    "message": "wait timed out",
    "calls_seen": 0,
    "min_calls": 1,
    "active": 1,
    "in_flight": [
      {
        "call_id": "call_1",
        "seq": 1,
        "method": "POST",
        "path": "/v1/responses",
        "api_family": "openai.responses",
        "upstream": "http://127.0.0.1:9/v1/responses",
        "started_at": "2026-10-22T07:00:00Z",
        "elapsed_ms": 1000,
        "phase": "awaiting_headers",
        "bytes_received": 0
      }
    ]
  }
}
```
