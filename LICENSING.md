# Licensing

This repository is AGPL-3.0 only: it contains the Go proxy. All clients are MIT
in their own repositories.

- Go proxy binary (`cmd/llmtestlab` and the proxy/server implementation it
  ships): AGPL-3.0, covered by the root [LICENSE](LICENSE).
- Go client: MIT, [llm-test-lab-go](https://github.com/barzoj-co/llm-test-lab-go).
- Python client: MIT, [llm-test-lab-python](https://github.com/barzoj-co/llm-test-lab-python).
- JS/TS client: MIT, [llm-test-lab-js](https://github.com/barzoj-co/llm-test-lab-js).

Using the proxy in your test pipeline does not affect your application's
license. Your app talks to the proxy over HTTP. The MIT client libraries are
importable without linking the AGPL proxy/server code.

The official OpenAI OpenAPI specification vendored at `api/openai/openapi.json`
is MIT-licensed (see `api/openai/LICENSE`). It is pinned to a specific
`openai/openai-openapi` commit recorded in `api/openai/SPEC_VERSION`. Generated
Go (`internal/openaiapi/gen.go`) and the rest of the proxy are AGPL-3.0-only.
oapi-codegen is an Apache-2.0 build-time tool only.
