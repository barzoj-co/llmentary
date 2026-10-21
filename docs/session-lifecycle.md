# Session lifecycle

How `GET /sessions/{id}`, `GET /sessions/{id}/wait`, `DELETE /sessions/{id}`,
and `GET /sessions` treat a session id.

| State | `GET /sessions/{id}` | `wait` `min_calls=0` | `wait` `min_calls>=1` | `DELETE` | `GET /sessions` list |
|---|---|---|---|---|---|
| Never created | `404` `session_not_found` | `404` `session_not_found` immediately | wait until `timeout`, then `504` `wait_timeout` | `204` (idempotent) | absent |
| Has calls | `200` session | `200` after quiet | `200` when enough finished calls are quiet | `204` | present |
| Deleted | `404` `session_not_found` | `404` `session_not_found` immediately | wait until `timeout`, then `504` `wait_timeout` | `204` (idempotent) | absent |

A never-created session is never a `200` with an empty `calls` array. Use
`min_calls>=1` when an async worker may still create the session.

Machine-readable contract: [`api/control/openapi.yaml`](../api/control/openapi.yaml).
Control API reference: [Proxy](proxy.md#get-sessionsidwait).

`GET /sessions[].spec` counts how each finished call was validated. Canonical
keys are snake_case (`checked`, `clean`, `findings`, `request_only`,
`not_checked`); PascalCase aliases are also serialized. See
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
    "message": "no calls recorded for session \"ghost\"; known sessions: GET /sessions"
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
    "min_calls": 1
  }
}
```
