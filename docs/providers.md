# Providers and SDK wiring

`llmentary` records any HTTP traffic it proxies. It only normalizes OpenAI-wire Chat
Completions and Responses calls. Everything else is raw passthrough.

## OpenAI Chat Completions

Chat Completions calls are parsed when the upstream request is `POST` and the final
upstream path ends with `/chat/completions`.

Typical upstream:

```bash
llmentary --upstream openai=https://api.openai.com/v1
```

SDK base URL for named routing:

```text
http://127.0.0.1:7777/u/openai
```

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

## OpenRouter

OpenRouter uses an OpenAI-compatible Chat Completions stream shape. Extra fields
absent from the OpenAI spec (`provider`, `native_finish_reason`, `usage.cost`,
`delta.reasoning`) are read by a thin extension layer from raw JSON. The parser
also tolerates:

- SSE keep-alive comments such as `: OPENROUTER PROCESSING`;
- chunks with `choices: []`;
- final usage chunks.

Those extras appear as `served_by`, `native_finish_reason`, `usage.cost_usd`,
and `reasoning.chars`.

Configure it as an OpenAI-compatible upstream:

```bash
llmentary --upstream openrouter=https://openrouter.ai/api/v1
```

Use base URL:

```text
http://127.0.0.1:7777/u/openrouter
```

## Other OpenAI-compatible providers

Providers that expose OpenAI-compatible `/chat/completions` or `/responses` endpoints can
be routed the same way. The normalizer is tolerant, not exhaustive: unknown fields are
preserved only in raw bodies, and provider-specific fields are parsed only when the code
knows them.

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

### `openai-go`

```go
// Go client: github.com/barzoj-co/llmentary-go
lab := llmentary.New(t)

client := openai.NewClient(
    option.WithAPIKey(os.Getenv("OPENAI_API_KEY")),
    option.WithBaseURL(lab.OpenAIBaseURL("openai")),
    option.WithHTTPClient(lab.HTTPClient()),
)
```

`lab.HTTPClient()` wraps the transport and injects `X-Test-Session` on every request.

### `openai-python`

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
