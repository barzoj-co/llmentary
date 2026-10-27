# OpenAI-compatible providers

This guide is for a developer who already talks to an OpenAI-compatible
upstream (DeepSeek, Gemini's OpenAI surface, OpenRouter, Groq, Mistral, Ollama,
llama.cpp, vLLM, …) and wants `llmentary` in front of it. It matches v0.5.x
. Fixture-verified rows live in [compat-matrix.md](compat-matrix.md). The proxy does not translate request or response
formats. It forwards the body and auth headers unchanged and records a tee copy.

What is validated in `--spec-validation enforce` depends on the per-upstream
**spec target**. `--spec-validation` defaults to `warn` (record findings, still
forward). This guide uses `enforce` so you can see what 400s locally and what
does not.

Related reference: [Providers and SDK wiring](providers.md),
[Proxy configuration](proxy.md).

## Point the proxy at a compatible upstream

`--upstream` / `LLMENTARY_UPSTREAMS` is `name=https://host/base` (repeatable).
A bare URL is stored as `default`. There is no config file.

`--spec-target` / `LLMENTARY_SPEC_TARGETS` is `name=openai` or
`name=openai-compatible` (repeatable). The name must match an upstream. Invalid
values and unknown names exit non-zero. When omitted, the target is chosen from
the upstream **host**, not the route name:

| Upstream host | Default `--spec-target` |
|---|---|
| `api.openai.com` | `openai` |
| `*.openai.azure.com`, `*.cognitiveservices.azure.com`, `*.services.ai.azure.com` | `openai` |
| everything else (including `api.deepseek.com`, localhost, IPs) | `openai-compatible` |

`--upstream openai=https://api.deepseek.com` is therefore still
`openai-compatible` unless you override it with `--spec-target openai=openai`.

`GET /config` reports `spec_targets`. For a recorded call that matches a
spec operation with validation enabled, `normalized.validation.target`
stores the resolved target. Passthrough calls and `--spec-validation=off`
omit that field.

```bash
llmentary \
  --listen 127.0.0.1:7777 \
  --control 127.0.0.1:7778 \
  --upstream ds=https://api.deepseek.com \
  --spec-target ds=openai-compatible \
  --spec-validation enforce
```

Equivalent environment:

```bash
export LLMENTARY_LISTEN=127.0.0.1:7777
export LLMENTARY_CONTROL_LISTEN=127.0.0.1:7778
export LLMENTARY_UPSTREAMS=ds=https://api.deepseek.com
export LLMENTARY_SPEC_TARGETS=ds=openai-compatible
export LLMENTARY_SPEC_VALIDATION=enforce
llmentary
```

```bash
curl -sS http://127.0.0.1:7778/config
```

`--spec-profile` defaults to `relaxed`. `--spec-validation-max-bytes` defaults
to `8388608`. Those flags are unchanged from [proxy.md](proxy.md).

App / SDK base URL (one upstream named `ds`, or any single upstream):

```text
http://127.0.0.1:7777/u/ds
http://127.0.0.1:7777/s/weather/u/ds
http://127.0.0.1:7777/s/weather/v1
```

Set `OPENAI_BASE_URL` (or the SDK `base_url` / `baseURL`) to one of those. The
proxy does not need the provider key; keep using the key the app already sends
as `Authorization`.

## What `openai-compatible` validate vs reject

Target `openai` is the closed-world OpenAI schema: unknown fields and
OpenAI-invalid enum values reject in enforce.

Target `openai-compatible` type-rejects a core subset of known OpenAI fields:
`model`, `messages`, `tools`, `max_tokens`, `max_completion_tokens`,
`temperature`, `top_p`, `n`, `stream`, `presence_penalty`, `frequency_penalty`,
`seed`, message `role`, and incompatible message `content` types. It does **not**
400 vendor fields that compatible hosts accept or require. Other OpenAI-schema
fields with a wrong type are forwarded as warnings.

| Request finding | `openai` + enforce | `openai-compatible` + enforce |
|---|---|---|
| Core known-field type / required (`"max_tokens":"16"`, `"messages":"hi"`, missing user `content`) | local 400, upstream not called | local 400, upstream not called |
| Other OpenAI-schema field type (`"top_logprobs":"5"`) | local 400, upstream not called | forwarded; `spec_violations` with `severity=warning` |
| Duplicate keys, UTF-8 BOM, trailing data | local 400 | local 400 |
| Unknown properties (`thinking`, `top_k`, `prefix`, `reasoning_content`, `reasoning_details`, `extra_content`, `extra_body`, …) | local 400 | forwarded; recorded as `ignored_fields` `kind=vendor_field`, `severity=info`, `action=report_only` |
| Extended enums (`tool_choice:"any"`, …) | local 400 | forwarded; `spec_violations` with `severity=warning` |
| OpenAI-only schema-keyword lints (`minLength`, `examples`, `oneOf`, `allOf`, unresolved `$ref`, Responses `strict_fallback_predicted`, …) | `reject_in_enforce` | rematched to `severity=info` / `report_only` |
| Response-side `spec_violations` | recorded, never reject; counted in `spec=findings:N` | summarized as `response_shape` (`severity=info`, one row per pointer+keyword); never WARN / never counted in `spec=findings:N`. Vendor extras produce no finding. See [Response-side findings](#response-side-findings) |

Unknown fields use hint
`Forwarded to the upstream unchanged. This field is not in the OpenAI request schema.`
Rematched OpenAI-only lints use
`This finding is informational for the selected upstream; verify the behavior with that upstream.`

Request-side reasoning echo is counted on `normalized.reasoning_in`
(`assistant_turns`, `with_reasoning`, `chars`). Assert
`normalized.reasoning_in.with_reasoning == normalized.reasoning_in.assistant_turns`
when every assistant turn carried reasoning.

## DeepSeek

Official base: `https://api.deepseek.com` (no version segment). The client's
`/v1` is kept. See [Version-segment join](#version-segment-join).

| Client base | Final upstream URL |
|---|---|
| `{proxy}/u/ds` + `/chat/completions` | `https://api.deepseek.com/chat/completions` |
| `{proxy}/u/ds/v1` or `{proxy}/v1` | `https://api.deepseek.com/v1/chat/completions` |
| `{proxy}/u/ds/beta` | `https://api.deepseek.com/beta/chat/completions` |

DeepSeek documents both origin and `/v1`. Prefix completion requires the beta
base ([Chat prefix completion](https://api-docs.deepseek.com/guides/chat_prefix_completion)):
`base_url="https://api.deepseek.com/beta"`.

Host `api.deepseek.com` defaults to `openai-compatible`. `--spec-target ds=openai-compatible`
makes that explicit so enforce does not 400 `reasoning_content`, `prefix`, or
`thinking`. Target `openai` 400s those fields.

### Thinking mode with tools

[Thinking mode](https://api-docs.deepseek.com/guides/thinking_mode/) returns
chain-of-thought on the assistant message as `reasoning_content` (same level as
`content`). Toggle on the wire with `thinking` (OpenAI SDK:
`extra_body={"thinking": {"type": "enabled"}}`) and/or `reasoning_effort`.
See also the [Chat Completions schema](https://api-docs.deepseek.com/api/create-chat-completion).

When the request carries `tools`, DeepSeek requires every previous assistant
turn's `reasoning_content` to be passed back — including turns that did not
call a tool. Omitting it is a DeepSeek 400. Without `tools`, passback is
ignored and not concatenated.

```json
{
  "model": "deepseek-chat",
  "thinking": {"type": "enabled"},
  "messages": [
    {"role": "user", "content": "Weather in Paris?"},
    {
      "role": "assistant",
      "content": "I'll check.",
      "reasoning_content": "Need a weather tool.",
      "tool_calls": [
        {
          "id": "call_1",
          "type": "function",
          "function": {"name": "get_weather", "arguments": "{\"city\":\"Paris\"}"}
        }
      ]
    },
    {"role": "tool", "tool_call_id": "call_1", "content": "{\"temp\":18}"}
  ],
  "tools": [
    {
      "type": "function",
      "function": {
        "name": "get_weather",
        "parameters": {
          "type": "object",
          "properties": {"city": {"type": "string"}}
        }
      }
    }
  ]
}
```

```bash
curl -sS http://127.0.0.1:7777/s/weather/u/ds/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer sk-test' \
  -d @- <<'EOF'
{
  "model": "deepseek-chat",
  "thinking": {"type": "enabled"},
  "messages": [
    {"role": "user", "content": "Weather in Paris?"},
    {
      "role": "assistant",
      "content": "I'll check.",
      "reasoning_content": "Need a weather tool.",
      "tool_calls": [
        {
          "id": "call_1",
          "type": "function",
          "function": {"name": "get_weather", "arguments": "{\"city\":\"Paris\"}"}
        }
      ]
    },
    {"role": "tool", "tool_call_id": "call_1", "content": "{\"temp\":18}"}
  ],
  "tools": [
    {
      "type": "function",
      "function": {
        "name": "get_weather",
        "parameters": {
          "type": "object",
          "properties": {"city": {"type": "string"}}
        }
      }
    }
  ]
}
EOF
```

### Prefix continuation

Last message must be `role=assistant` with `prefix: true`
([prefix field](https://api-docs.deepseek.com/api/create-chat-completion),
[guide](https://api-docs.deepseek.com/guides/chat_prefix_completion)). Point the
SDK at the beta base so the path is `/beta/chat/completions`. DeepSeek's sample
uses a markdown code-fence string as the prefix; the field shape is the same.

```json
{
  "model": "deepseek-chat",
  "messages": [
    {"role": "user", "content": "Write a Python hello world"},
    {"role": "assistant", "content": "def hello():", "prefix": true}
  ]
}
```

```bash
curl -sS http://127.0.0.1:7777/s/weather/u/ds/beta/chat/completions \
  -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer sk-test' \
  -d '{"model":"deepseek-chat","messages":[{"role":"user","content":"Write a Python hello world"},{"role":"assistant","content":"def hello():","prefix":true}]}'
```

`OPENAI_BASE_URL=http://127.0.0.1:7777/s/weather/u/ds/beta` is the SDK form of
that path.

### `/beta/chat/completions` and `/beta/completions`

`beta` is in the default path allow-list, so these work as bare paths as well
as `/u/ds/...`:

| Proxy path | `api_family` |
|---|---|
| `POST /beta/chat/completions` or `/u/ds/beta/chat/completions` | `openai.chat` (validated + normalized) |
| `POST /beta/completions` or `/u/ds/beta/completions` | `passthrough` (recorded; path does not end in `/chat/completions` or `/responses`) |

```bash
curl -sS http://127.0.0.1:7777/s/weather/u/ds/beta/completions \
  -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer sk-test' \
  -d '{"model":"deepseek-chat","prompt":"def hello():"}'
```

### Known-field types still reject

Compatible target does not turn enforce off. This is still a local 400 and the
upstream is not called:

```json
{"model":"deepseek-chat","messages":[{"role":"user","content":"hi"}],"max_tokens":"16"}
```

```bash
curl -sS -o /tmp/llmentary-compat-max-tokens.out -w '%{http_code}\n' \
  http://127.0.0.1:7777/s/weather/u/ds/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"deepseek-chat","messages":[{"role":"user","content":"hi"}],"max_tokens":"16"}'
```

## Gemini (OpenAI-compatible surface)

```bash
llmentary \
  --listen 127.0.0.1:7777 \
  --control 127.0.0.1:7778 \
  --upstream gemini=https://generativelanguage.googleapis.com/v1beta/openai \
  --spec-target gemini=openai-compatible \
  --spec-validation enforce
```

Upstream base already contains `v1beta`. A client `{proxy}/v1` drops that
leading version so the join is `/v1beta/openai/chat/completions`, not
`/v1beta/openai/v1/...`. `{proxy}/u/gemini` and `{proxy}/u/gemini/v1` do the
same. `v1beta` is allow-listed, so a bare `/v1beta/openai/chat/completions`
is also accepted.

Gemini 3/4 tool follow-ups require
`tool_calls[].extra_content.google.thought_signature` on the next request.
Target `openai-compatible` forwards `extra_content` as `vendor_field`. The raw
trace keeps it (including parallel calls where only the first call carries a
signature).

```json
{
  "model": "gemini-fixture",
  "messages": [
    {
      "role": "assistant",
      "content": null,
      "tool_calls": [
        {
          "id": "c1",
          "type": "function",
          "function": {"name": "get_weather", "arguments": "{}"},
          "extra_content": {"google": {"thought_signature": "sig"}}
        }
      ]
    }
  ]
}
```

## OpenRouter

```bash
llmentary \
  --listen 127.0.0.1:7777 \
  --control 127.0.0.1:7778 \
  --upstream openrouter=https://openrouter.ai/api/v1 \
  --spec-target openrouter=openai-compatible \
  --spec-validation enforce
```

`{proxy}/u/openrouter` and `{proxy}/v1` both join to
`https://openrouter.ai/api/v1/chat/completions`. Assistant
`reasoning_details` is forwarded (same compatible-target rule as
`reasoning_content`).

```json
{
  "model": "openai/gpt-4.1-mini",
  "messages": [
    {
      "role": "assistant",
      "content": "x",
      "reasoning_details": [{"type": "reasoning.summary", "text": "n"}]
    }
  ]
}
```

OpenRouter stream extras (`provider`, `native_finish_reason`, final-chunk
`usage.cost`, `: OPENROUTER PROCESSING` comments) are recorded; see
[providers.md](providers.md#openrouter).

## Groq

```bash
llmentary \
  --listen 127.0.0.1:7777 \
  --control 127.0.0.1:7778 \
  --upstream groq=https://api.groq.com/openai/v1 \
  --spec-target groq=openai-compatible \
  --spec-validation enforce
```

`{proxy}/v1` joins to `https://api.groq.com/openai/v1/chat/completions`. When a
stream's last chunk has `x_groq.usage` and no top-level `usage`, the normalizer
records `usage` with `source: "x_groq"`. Top-level
`usage` wins when both are present.

## Mistral

```bash
llmentary \
  --listen 127.0.0.1:7777 \
  --control 127.0.0.1:7778 \
  --upstream mistral=https://api.mistral.ai/v1 \
  --spec-target mistral=openai-compatible \
  --spec-validation enforce
```

Mistral prefix completion uses the same last-assistant `prefix: true` field
shape as DeepSeek. Target `openai-compatible` forwards it. Use
`{proxy}/u/mistral` or `{proxy}/v1`.

## Local engines (Ollama, llama.cpp, vLLM)

```bash
llmentary \
  --listen 127.0.0.1:7777 \
  --control 127.0.0.1:7778 \
  --upstream ollama=http://127.0.0.1:11434/v1 \
  --upstream llama=http://127.0.0.1:8080 \
  --upstream vllm=http://127.0.0.1:8000/v1 \
  --spec-target ollama=openai-compatible \
  --spec-target llama=openai-compatible \
  --spec-target vllm=openai-compatible \
  --spec-validation enforce
```

| Engine | Typical OpenAI-wire base | Native path (named routing) |
|---|---|---|
| Ollama | `http://127.0.0.1:11434/v1` | — |
| llama.cpp | `http://127.0.0.1:8080/v1` | `/u/llama/completion` |
| vLLM | `http://127.0.0.1:8000/v1` | `/u/vllm/tokenize` |

`POST` paths that end in `/chat/completions` are validated and normalized.
Native paths are `passthrough`. Local hosts default to `openai-compatible`, so
vendor sampling fields (`top_k`, `min_p`, `chat_template_kwargs`,
`cache_prompt`, `keep_alive`) and grammar keywords that OpenAI Structured
Outputs rejects are forwarded / rematched instead of 400. Together / Ollama
`finish_reason: "eos"` maps to `stop`; the raw value stays in
`native_finish_reason`.

## Path allow-list

After `/s/{session}` and `/u/{name}` are stripped, a **bare** path's first
segment must be one of: `v1`, `openai`, `api`, `chat`, `responses`,
`completions`, `embeddings`, `models`, `files`, `audio`, `images`,
`moderations`, `batches`, `uploads`, `vector_stores`, `assistants`, `threads`,
`realtime`, `beta`, `v1beta`.

`--allow-path-prefix` / `LLMENTARY_ALLOW_PATH_PREFIXES` extends that list
(repeatable flag; comma-separated env). Explicit `/u/{name}/...` skips the
allow-list.

```bash
llmentary \
  --listen 127.0.0.1:7777 \
  --control 127.0.0.1:7778 \
  --upstream llama=http://127.0.0.1:8080 \
  --allow-path-prefix tokenize
```

Bare `/tokenize` then forwards. Without the flag it is a local `404`
(`type`/`code` `llmentary_unknown_path`) and is not forwarded. The body mentions
`--allow-path-prefix tokenize` and `LLMENTARY_ALLOW_PATH_PREFIXES` (or
`did you mean /s/<segment>/...?` when the extra segment looks like a missing
session prefix). Prefer `/u/llama/tokenize` when the route is named.

## Version-segment join

The proxy overlaps shared path prefixes. `/s/{session}` and `/u/{name}` are
stripped first. When the remaining request starts with a version-like segment
(`v1`, `v1beta`, `v1alpha`) **and** the upstream base already contains one, the
client's leading version is dropped. The rewrite logs a one-time WARN.
Upstreams without a version segment keep the client's `/v1`.

| Upstream form | Client base | Final path |
|---|---|---|
| `https://api.deepseek.com` | `{proxy}/v1` | `/v1/chat/completions` |
| `https://generativelanguage.googleapis.com/v1beta/openai` | `{proxy}/v1` | `/v1beta/openai/chat/completions` |
| `https://generativelanguage.googleapis.com/v1beta/openai` | `{proxy}/u/gemini/v1` | `/v1beta/openai/chat/completions` |
| `https://api.groq.com/openai/v1` | `{proxy}/v1` | `/openai/v1/chat/completions` |
| `https://openrouter.ai/api/v1` | `{proxy}/v1` | `/api/v1/chat/completions` |

## Stream normalization

Applied to any OpenAI-wire Chat Completions stream (`POST` path ending in
`/chat/completions`, SSE `data: {json}` + `data: [DONE]`):

- Groq last-chunk `x_groq.usage` → recorded `usage` with `source: "x_groq"` when
  top-level `usage` is absent.
- `finish_reason: "eos"` (Together, Ollama) → `stop`; raw value in
  `native_finish_reason`.
- HTTP-200 stream errors are classified (`server`, `rate_limit`,
  `content_filter`, `unknown`) on
  `error.normalized`.

The client still receives the upstream bytes unchanged.

## OpenAI-only lints

These ignored-field kinds are OpenAI Structured Outputs / strict-mode checks.
Target `openai` keeps them `reject_in_enforce`. Target `openai-compatible`
rematches them to `severity=info` / `report_only`:

- `unsupported_schema_keyword` (documented-unsupported: `allOf`, `not`, `if` /
  `then` / `else`, …; also `minLength` and friends on fine-tuned strict tools)
- `unknown_schema_keyword` (`oneOf`, `examples`, `enumDescriptions`, `x-*`, …)
- `unsupported_format`
- `suspicious_enum`
- `strict_fallback_predicted` / `strict_fallback`
- `unresolved_ref`

`default` on a schema is a structural keyword, not this rematch class.
Request-side OpenAI-only lints rematch as above. Response-side
`ignored_fields` `strict_fallback` also rematches (see below); other
response-side findings do not.

## Response-side findings

Response-side `spec_violations` never reject. Target `openai` stores them as
compiled and counts them in `spec=findings:N`. Target `openai-compatible`
summarizes remaining response findings as one `response_shape` row per
distinct pointer+keyword (`severity=info`, with `count`). Those rows are
stored on the call but are not WARN-logged and are not counted in
`spec=findings:N`. Extra vendor fields (`timings`, `x_groq`, `provider`, …)
produce no finding. Omitted OpenAI-required-nullable keys stay
`ignored_fields` `kind=compatible_optional`.

Both targets cap stored response findings at `--spec-response-findings-max`
(default 50). Overflow sets `validation.truncated`.

Response-side `ignored_fields` `strict_fallback` is the exception for
OpenAI-only lints: when a successful Responses body confirms a tool that
omitted `strict`, rematching sets `severity=info` and `action=report_only`.

## Client examples

Change only `OPENAI_BASE_URL` or the SDK `base_url` / `baseURL`. Auth and the
rest of the client stay as they are. For prefix completion, use the `/beta`
suffix instead of `/v1`.

Go:

```go
os.Setenv("OPENAI_BASE_URL", "http://127.0.0.1:7777/s/weather/u/ds")
client := openai.NewClient()
```

```go
client := openai.NewClient(
    option.WithBaseURL("http://127.0.0.1:7777/s/weather/u/ds"),
)
```

Python:

```python
os.environ["OPENAI_BASE_URL"] = "http://127.0.0.1:7777/s/weather/u/ds"
client = OpenAI()
```

```python
client = OpenAI(base_url="http://127.0.0.1:7777/s/weather/u/ds")
```

JavaScript:

```js
process.env.OPENAI_BASE_URL = "http://127.0.0.1:7777/s/weather/u/ds";
const client = new OpenAI();
```

```js
const client = new OpenAI({
  baseURL: "http://127.0.0.1:7777/s/weather/u/ds",
});
```

Prefix continuation (any client):

```text
OPENAI_BASE_URL=http://127.0.0.1:7777/s/weather/u/ds/beta
```
