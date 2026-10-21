# Isolation test vectors

Normative fixtures for [`docs/isolation-spec.md`](../../docs/isolation-spec.md).
Clients copy this directory and fail CI if the copy drifts from the pinned
proxy version.

Each file is one `X.isolated_from(Y)` check.

```json
{
  "id": "same-canned-question",
  "description": "...",
  "allow_empty": false,
  "min_chars": 8,
  "ignore_texts": [],
  "x": { "id": "sess-x", "calls": [ /* Call-shaped objects */ ] },
  "y": { "id": "sess-y", "calls": [ /* Call-shaped objects */ ] },
  "expect": {
    "isolated": true,
    "message_contains": []
  }
}
```

`calls[]` use the control-API field names (`id`, `session_id`, `seq`).
`raw_request` may be a JSON object (convenience in these fixtures) or the
base64 string the proxy stores on a live `Call`. User turns are read from
Chat `messages` or Responses `input` as defined in the spec.

`message_contains` is a list of fragments that must all appear in the failure
text when `isolated` is false. Pass cases leave it empty.
