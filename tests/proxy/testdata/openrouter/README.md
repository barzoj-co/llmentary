# OpenRouter fixture

Hand-written OpenRouter-shaped Chat Completions SSE, assembled from OpenRouter's
public API docs (not a live capture). Used by `TestU5OpenRouterFixture`.

Sources:

- https://openrouter.ai/docs/api/reference/streaming
- https://openrouter.ai/docs/cookbook/administration/usage-accounting
- https://openrouter.ai/docs/api/reference/overview

The stream includes:

- SSE keep-alive comments (`: OPENROUTER PROCESSING`)
- extra `provider` field on chunks
- tool-call argument deltas
- a terminal usage frame with one content-free choice that repeats
  `finish_reason` / `native_finish_reason`, plus `usage` (including `cost`)
- the OpenAI `data: [DONE]` terminator

This fixture is **fixture-verified**, not live-verified. Do not treat it as a
recording of a billed OpenRouter session.
