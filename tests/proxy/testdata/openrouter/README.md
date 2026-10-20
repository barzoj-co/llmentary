# OpenRouter fixture

Hand-written OpenRouter-shaped Chat Completions SSE, assembled from OpenRouter's
public API docs (not a live capture). Used by `TestU5OpenRouterFixture`.

Sources:

- https://openrouter.ai/docs/api/reference/streaming
- https://openrouter.ai/docs/guides/features/usage-accounting
- https://openrouter.ai/docs/api/reference/overview

The stream includes:

- SSE keep-alive comments (`: OPENROUTER PROCESSING`)
- extra `provider` field on chunks
- tool-call argument deltas
- a final usage chunk (`choices: []` plus `usage`, including `cost`)
- the OpenAI `data: [DONE]` terminator

This fixture is **fixture-verified**, not live-verified. Do not treat it as a
recording of a billed OpenRouter session.
