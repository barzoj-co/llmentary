# Providers and SDK wiring

`llmentary` records any HTTP traffic it proxies. It only normalizes OpenAI-wire Chat
Completions and Responses calls. Everything else is raw passthrough.

## OpenAI Chat Completions

Chat Completions calls are parsed when the upstream request is `POST` and the final
upstream path ends with `/chat/completions`.

Typical upstream (this is also the default when `--upstream` is omitted):

```bash
llmentary --upstream openai=https://api.openai.com/v1
```

SDK base URL for named routing:

```text
http://127.0.0.1:7777/u/openai
```

OpenAI-style SDK bases (`{proxy}/v1`) work with the same upstream. The proxy overlaps
shared path prefixes so `https://api.openai.com/v1` plus `{proxy}/v1/chat/completions`
forwards to `https://api.openai.com/v1/chat/completions` once, without repeating the
API version segment.

The parser records:

- requested and returned model;
- system/developer prompt text and SHA-256 hash;
- message count and prompt character count;
- declared function tools and schema hashes;
- tool results sent back in later requests;
- assistant output text;
- tool calls, including streamed argument deltas;
- reasoning text length when upstream fields expose it;
- raw/native finish reasons;
- usage from response bodies or final stream chunks;
- content-filter result chunks where present;
- HTTP and HTTP-200 stream errors.

## OpenAI Responses

Responses calls are parsed when the upstream request is `POST` and the final upstream path
ends with `/responses`.

Typical upstream:

```bash
llmentary --upstream openai=https://api.openai.com/v1
```

The parser records:

- requested and returned model;
- `instructions` plus system/developer input items;
- `previous_response_id` and whether server-side context is in use;
- declared tools, including built-in/custom tool shapes when recognizable;
- `function_call` and `custom_tool_call` output items;
- `function_call_output` items fed back in the request;
- output text;
- status-derived finish reasons;
- usage from response bodies or terminal stream events;
- response errors and HTTP-200 stream errors.

Responses stream parsing uses generated `ResponseStreamEvent.ValueByDiscriminator()`
with wire `type` values (`response.output_text.delta`, not schema names). It
recognizes `response.completed`, `response.failed`, `response.incomplete`,
`response.output_item.done`, `response.output_text.delta`,
`response.function_call_arguments.delta`, `response.refusal.delta/done`, and
`error`.

The official spec's Responses stream `error` event has top-level `code` and
`message` (`{"type":"error","code":"...","message":"..."}`). Some payloads nest
`{"type":"error","error":{...}}`. The proxy accepts both and sets
`normalized.error.mid_stream=true`. Live OpenAI HTTP 400/401 (verified in
`tests/e2e`) use the nested `ErrorResponse` object, not a stream frame:

```json
{"error":{"message":"...","type":"invalid_request_error","code":"...","param":"..."}}
```

A 401 may also include a sibling `"status": 401` and `"param": null`. Chat
Completions mid-stream errors are description-only in the spec
(`data: {"error":{...}}` inside HTTP 200) and are handled by hand, as is the
literal `data: [DONE]` terminator.

## Azure OpenAI

Azure works as long as the final path is OpenAI-wire compatible.

Classic dated `api-version` deployment URL:

```bash
llmentary --upstream azure=https://my-resource.openai.azure.com
```

```text
http://127.0.0.1:7777/u/azure/openai/deployments/my-deployment/chat/completions?api-version=2024-10-21
  -> https://my-resource.openai.azure.com/openai/deployments/my-deployment/chat/completions?api-version=2024-10-21
```

The proxy records:

- `upstream.api_version` from the `api-version` query parameter;
- `upstream.deployment` from `/deployments/{deployment}/...`;
- `llmentary.azure.api_version` and `llmentary.azure.deployment` OTel attributes.

Routing rewrites `/openai/deployments/{id}/chat/completions`, `/openai/v1/...`,
`/api/v1/...`, and bare `/chat/completions` or `/responses` onto the generated
router for matching only. The original path, query (`api-version`), and headers
(`api-key`, `Authorization`) go upstream unchanged. `api-key` is hashed
(`sha256:...`) in the recorded trace.

Azure `/openai/v1` OpenAI-compatible URLs also work because the final path still ends in
`/chat/completions` or `/responses`:

```bash
llmentary --upstream azure=https://my-resource.openai.azure.com/openai/v1
```

```text
http://127.0.0.1:7777/u/azure/chat/completions
  -> https://my-resource.openai.azure.com/openai/v1/chat/completions
```

Azure content-filter streams can include empty `choices` chunks with prompt filter
metadata. The Chat Completions stream parser tolerates those chunks and continues
assembling later tool-call deltas. Per-choice `content_filter_results` are captured in
`normalized.content_filter`.

### `openai-go` Azure HTTP note

The `openai-go` Azure middleware protects Azure credentials from being sent over plain
HTTP to non-loopback hosts. A local proxy at `http://127.0.0.1:7777` is loopback. Docker
or CI topologies that access the proxy through a non-loopback HTTP hostname may need a
different wiring strategy, such as an OpenAI-compatible Azure `/openai/v1` client pointed
at the proxy or a future TLS listener.

## Pick one: upstream form × client base

The proxy owns the path-join rule. Both upstream styles and both client styles are
correct; pick any cell.

| Upstream form | Client base URL | Final URL |
|---|---|---|
| `https://api.openai.com/v1` | `{proxy}/v1` | `https://api.openai.com/v1/chat/completions` |
| `https://api.openai.com/v1` | `{proxy}/u/openai` | `https://api.openai.com/v1/chat/completions` |
| `https://api.openai.com` | `{proxy}/v1` | `https://api.openai.com/v1/chat/completions` |
| `https://api.openai.com` | `{proxy}/u/openai/v1` | `https://api.openai.com/v1/chat/completions` |
| `https://openrouter.ai/api/v1` | `{proxy}/u/openrouter` | `https://openrouter.ai/api/v1/chat/completions` |
| `https://openrouter.ai/api/v1` | `{proxy}/v1` | `https://openrouter.ai/api/v1/chat/completions` |
| `https://openrouter.ai/api/v1` | `{proxy}/s/{id}/u/openrouter` | `https://openrouter.ai/api/v1/chat/completions` |
| `https://my-resource.openai.azure.com/openai/v1` | `{proxy}/u/azure` | `https://my-resource.openai.azure.com/openai/v1/chat/completions` |
| `https://generativelanguage.googleapis.com/v1beta/openai` | `{proxy}/v1` | `https://generativelanguage.googleapis.com/v1beta/openai/chat/completions` |
| `https://generativelanguage.googleapis.com/v1beta/openai` | `{proxy}/u/gemini/v1` | `https://generativelanguage.googleapis.com/v1beta/openai/chat/completions` |
| `https://generativelanguage.googleapis.com/v1beta/openai` | `{proxy}/u/gemini` | `https://generativelanguage.googleapis.com/v1beta/openai/chat/completions` |
| `https://api.deepseek.com` | `{proxy}/v1` | `https://api.deepseek.com/v1/chat/completions` |
| `https://api.groq.com/openai/v1` | `{proxy}/v1` | `https://api.groq.com/openai/v1/chat/completions` |
| `https://api.fireworks.ai/inference/v1` | `{proxy}/v1` | `https://api.fireworks.ai/inference/v1/chat/completions` |

`{proxy}/s/{id}/...` is the path-prefix session form. The proxy strips `/s/{id}` and
`/u/{name}` before applying the join. Equal overlapping path segments are used once.
When the request starts with a version-like segment (`v1`, `v1beta`, `v1alpha`) and the
upstream base already contains one, that leading request segment is dropped so Gemini
`/v1beta/openai` plus `{proxy}/v1` does not become `/v1beta/openai/v1/...`. The rewrite
logs a one-time WARN. Upstreams without a version segment keep the client's `/v1`.
Model names and auth headers pass through unchanged.

## OpenRouter

**Recipe**

```bash
llmentary --upstream openrouter=https://openrouter.ai/api/v1
```

App / SDK base URL:

```text
{proxy}/u/openrouter
{proxy}/s/{id}/u/openrouter
```

- Model names such as `openai/gpt-4.1-mini` pass through unchanged.
- The `Authorization` header passes through unchanged. The proxy never needs the
  OpenRouter key.
- OpenRouter adds or omits fields the OpenAI spec does not require:
  - `provider` on chunks (recorded as `served_by`);
  - `native_finish_reason`;
  - `usage` (including `cost`) on the final SSE chunk, recorded as `usage` /
    `usage.cost_usd`;
  - keep-alive comments such as `: OPENROUTER PROCESSING` (recorded as SSE comments,
    ignored by the normalizer);
  - `delta.reasoning` (recorded as `reasoning.chars`);
  - mid-stream `error.code: "server_error"` with `finish_reason: "error"` (normalized
    `error.normalized: "server"`, `finish_reason: "error"`).
- Chunks with `choices: []` are tolerated.

**Verified status:** fixture-verified (`tests/proxy/testdata/openrouter/`,
`TestU5OpenRouterFixture`). Not live-verified (no OpenRouter key in this project).

## Spec target

`--spec-validation enforce` applies the pinned OpenAI request schema. That is
correct for OpenAI and Azure OpenAI, and wrong for every other OpenAI-compatible
host: those providers accept (and sometimes require) extra fields.

`--spec-target name=openai|openai-compatible` (env `LLMENTARY_SPEC_TARGETS`)
sets the rule per upstream. When omitted:

| Upstream host | Default target |
|---|---|
| `api.openai.com` | `openai` |
| `*.openai.azure.com`, `*.cognitiveservices.azure.com`, `*.services.ai.azure.com` | `openai` |
| everything else (localhost, IPs, OpenRouter, Ollama, DeepSeek, Gemini, …) | `openai-compatible` |

`GET /config` reports `spec_targets`. Each call stores `normalized.validation.target`.

`openai-compatible` still rejects known-field type errors (`"max_tokens":"16"`).
It forwards vendor fields (`top_k`, `thinking`, `extra_body`, `tool_choice:"any"`,
assistant `reasoning_content`, `reasoning_details`, `prefix`, Gemini
`tool_calls[].extra_content`, …) and records them as `vendor_field` info
findings instead of enforce 400s.

Request-side reasoning echo is counted on `normalized.reasoning_in`
(`assistant_turns`, `with_reasoning`, `chars`) so tests can assert that every
assistant turn passed reasoning back. `extra_content` on response tool calls
stays in the raw body and does not break tool-call assembly.

## Other OpenAI-compatible providers

Native provider paths that are not under `/v1` (llama.cpp `/completion`,
vLLM `/tokenize`, DeepSeek `/beta/chat/completions`, Gemini `/v1beta/openai/...`)
should be sent as `{proxy}/u/{name}/...`. Explicit `/u/{name}` routing skips the
bare-path allow-list; `POST` paths that still end in `/chat/completions` or
`/responses` are validated and normalized as OpenAI-wire.

Anything that speaks OpenAI-wire HTTP can be a named upstream. For the normalizer to
fill tool calls, usage, and finish reasons, the request path must end in
`/chat/completions` or `/responses` and the stream (if any) must use OpenAI SSE
framing (`data: {json}` chunks, Chat Completions ending with `data: [DONE]`).

| Provider | Typical upstream | Default spec target | Verified |
|---|---|---|---|
| Azure OpenAI | `https://{resource}.openai.azure.com` or `.../openai/v1` | `openai` | fixture-verified (classic deployment paths, `/openai/v1`, content-filter streams). Not live-verified here. |
| Groq | `https://api.groq.com/openai/v1` | `openai-compatible` | expected to work, not verified |
| Together | `https://api.together.xyz/v1` | `openai-compatible` | expected to work, not verified |
| vLLM | `http://{host}:8000/v1` | `openai-compatible` | expected to work, not verified |
| LiteLLM | `http://{host}:4000/v1` | `openai-compatible` | expected to work, not verified |
| Ollama | `http://{host}:11434/v1` | `openai-compatible` | expected to work, not verified |
| DeepSeek | `https://api.deepseek.com` | `openai-compatible` | fixture-verified passback of `reasoning_content` / `prefix`. Not live-verified here. |
| Gemini (OpenAI compat) | `https://generativelanguage.googleapis.com/v1beta/openai` | `openai-compatible` | fixture-verified `extra_content.google.thought_signature` on tool calls. Not live-verified here. |

### DeepSeek thinking passback

DeepSeek thinking + tools requires every previous assistant turn's
`reasoning_content` (and, for prefix continuation, `prefix: true`) to be sent
back. Behind llmentary, use `--spec-target ds=openai-compatible` (or rely on the
`api.deepseek.com` host default) so enforce does not 400 those fields. Assert
`normalized.reasoning_in.with_reasoning == normalized.reasoning_in.assistant_turns`
when every assistant turn carried reasoning.

### Gemini thought signatures

Gemini 3/4 via the OpenAI-compatible surface requires
`tool_calls[].extra_content.google.thought_signature` on the next request.
The raw trace keeps `extra_content` (including parallel calls where only the
first call carries a signature). Enforce with target `openai-compatible`
forwards it as `vendor_field`.

Cross-provider stream normalization (any OpenAI-wire host, not OpenRouter-only):

- Groq last-chunk `x_groq.usage` is recorded as `usage` with `source: "x_groq"`
  when top-level `usage` is absent.
- Together / Ollama `finish_reason: "eos"` maps to `stop`; the raw value stays
  in `native_finish_reason`.

The normalizer is tolerant, not exhaustive: unknown fields stay in the raw body, and
provider-specific fields are parsed only when the code knows them.

Provider name in OTel is inferred from the upstream host for a few hosts
(`api.openai.com`, Azure OpenAI host suffixes, Groq, DeepSeek, Mistral, xAI, and Gemini);
otherwise it falls back to the upstream route name.

## Raw passthrough for the rest

Non-OpenAI-wire calls are forwarded and recorded but use `api_family: "passthrough"`.
They do not get normalized tool calls, usage, or model fields unless future parsers add
support.

This includes Anthropic Messages in the current code. Anthropic Messages parsing is
planned for v2.

## Retries are recorded per attempt

The proxy sits behind the SDK. When an SDK retries, each retry is another inbound HTTP
request to the proxy and therefore another recorded call/span.

The proxy reads `x-stainless-retry-count` into `call.retry_count` and the OTel
`http.request.resend_count` attribute. If the header is missing or not an integer, retry
count is omitted.

In Go assertions (see [llmentary-go](https://github.com/barzoj-co/llmentary-go)),
`llmentary.ExcludeRetries()` excludes calls whose `retry_count` is present and greater
than zero.

## SDK wiring snippets

Use `X-Test-Session` for the clearest session tagging. W3C baggage with
`llmentary.session.id=<id>` also works when an SDK does not expose custom headers.
The Go, Python, and JS/TS clients inject that header for you.

### `openai-go`

```go
// Go client: github.com/barzoj-co/llmentary-go
lm := llmentary.New(t)

client := openai.NewClient(
    option.WithAPIKey(os.Getenv("OPENAI_API_KEY")),
    option.WithBaseURL(lm.OpenAIBaseURL("openai")),
    option.WithHTTPClient(lm.HTTPClient()),
)
```

`lm.HTTPClient()` wraps the transport and injects `X-Test-Session` on every request.
After the SDK call: `lm.Expect(ctx)...`.

### `openai-python`

The pytest plugin registers a fixture named `llmentary`:

```python
def test_weather(llmentary):
    client = OpenAI(
        api_key=os.environ["OPENAI_API_KEY"],
        base_url="http://127.0.0.1:7777/u/openai",
    )
    # Drive the real SDK. The fixture tags the session.
    llmentary.expect()
```

Without the client, tag the session yourself:

```python
from openai import OpenAI

session_id = "test-" + uuid.uuid4().hex
client = OpenAI(
    api_key=os.environ["OPENAI_API_KEY"],
    base_url="http://127.0.0.1:7777/u/openai",
    default_headers={"X-Test-Session": session_id},
)
```

Fetch the trace from the control API:

```python
requests.get(f"http://127.0.0.1:7778/sessions/{session_id}/wait").json()
```

### `openai-node`

Vitest:

```ts
import { llmentary } from "llmentary/vitest";
import OpenAI from "openai";

const lm = llmentary();
const client = new OpenAI({
  apiKey: process.env.OPENAI_API_KEY,
  baseURL: "http://127.0.0.1:7777/u/openai",
});
lm.expect();
lm.query({}).expect();
```

Jest:

```js
const { llmentary } = require("llmentary/jest");
const lm = llmentary();
```

Without the client, tag the session yourself:

```ts
import OpenAI from "openai";

const sessionID = crypto.randomUUID();
const client = new OpenAI({
  apiKey: process.env.OPENAI_API_KEY,
  baseURL: "http://127.0.0.1:7777/u/openai",
  defaultHeaders: { "X-Test-Session": sessionID },
});
```

### LangChain Python

When `base_url` or `OPENAI_BASE_URL` is custom, set `stream_usage=True` if you need token
usage in Chat Completions streams.

```python
from langchain_openai import ChatOpenAI

session_id = "test-" + uuid.uuid4().hex
llm = ChatOpenAI(
    model="gpt-4.1-mini",
    api_key=os.environ["OPENAI_API_KEY"],
    base_url="http://127.0.0.1:7777/u/openai",
    default_headers={"X-Test-Session": session_id},
    streaming=True,
    stream_usage=True,
)
```

### Vercel AI SDK

```ts
import { generateText } from "ai";
import { createOpenAI } from "@ai-sdk/openai";

const sessionID = crypto.randomUUID();
const openai = createOpenAI({
  apiKey: process.env.OPENAI_API_KEY,
  baseURL: "http://127.0.0.1:7777/u/openai",
  fetch: (input, init = {}) =>
    fetch(input, {
      ...init,
      headers: {
        ...Object.fromEntries(new Headers(init.headers)),
        "X-Test-Session": sessionID,
      },
    }),
});

await generateText({
  model: openai("gpt-4.1-mini"),
  prompt: "Call get_weather for Paris.",
});
```
