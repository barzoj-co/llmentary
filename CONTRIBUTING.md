# Contributing

Thanks for working on `llm-test-lab`.

## Requirements

- Go 1.26.x, matching `go.mod` and the GitHub Actions workflows.
- Standard Go tooling (`go test`, `gofmt`).

## OpenAPI spec and codegen

Chat Completions and Responses are generated from a pinned copy of the official
OpenAI OpenAPI spec.

- `api/openai/openapi.json` is the full vendored spec.
- `api/openai/SPEC_VERSION` records the `openai/openai-openapi` commit SHA,
  `info.version`, and sha256 of that file.
- `api/openai/operations.txt` lists the `operationId`s we generate.
- `api/openai/overlay.yaml` is an OpenAPI Overlay 1.0 used only for codegen
  name collisions (for example `CreateChatCompletionResponseObject` vs the
  `object` enum, and the same for embeddings). Every action has a "why"
  comment. Discriminator mapping for `ResponseStreamEvent` is **not** an
  overlay action: `tools/specfilter` injects it via
  `ensureDiscriminatorMappings`.
- `make gen` filters the spec (`tools/specfilter`) and runs pinned
  `oapi-codegen` v2.8.0 (`go tool oapi-codegen`, never `@latest`).
- CI job `codegen-check` runs `make gen`, `go mod tidy`, and `git diff --exit-code`.

Update the vendored spec only through an explicit PR:

```bash
make spec-update SPEC_SHA=<full openai/openai-openapi commit sha>
```

That downloads `openapi.json` from the commit, refreshes `SPEC_VERSION`, and
regenerates `internal/openaiapi/gen.go`.

To add an endpoint, append its `operationId` to `api/openai/operations.txt`,
run `make gen`, and implement the new method on `openAIHandlers`. There is a
compile-time assertion:

```go
var _ openaiapi.StrictServerInterface = (*openAIHandlers)(nil)
```

Do not embed Unimplemented stubs. If the method is missing, `go build` fails.

If generated code does not compile, prefer an overlay action in
`api/openai/overlay.yaml` (with a why-comment) over editing `gen.go`. Hand-edits
to `gen.go` fail `codegen-check`.

## Local checks

Run unit and fixture tests for both Go modules:

```bash
go test ./...
(cd pkg/llmtestlab && go test ./...)
(cd tests/proxy && go test ./...)
(cd examples/go && go test ./...)
```

Run real-provider e2e tests:

```bash
(cd tests/e2e && OPENAI_API_KEY=sk-... go test -v -tags=e2e ./...)
```

Optional:

```bash
(cd tests/e2e && OPENAI_E2E_MODEL=gpt-4.1-mini OPENAI_API_KEY=sk-... go test -v -tags=e2e ./...)
```

The e2e tests use a real OpenAI key, start an in-process proxy/control server with
`httptest`, call Chat Completions and Responses, and assert through the Go client.

Before sending code changes, format Go files:

```bash
gofmt -w ./cmd ./internal ./pkg ./examples
```

CI also runs `golangci-lint-action@v8`.

## Documentation rule

Document only behavior implemented in the current code. In particular:

- Only the Go proxy binary is AGPL-3.0. `pkg/llmtestlab` and the separate Python/JS
  client repos are MIT. Keep the nested Go client module importable without
  proxy/server packages. See [LICENSING.md](LICENSING.md).
- The example and test modules carry their own local `replace` directives for the root
  proxy module and MIT client module. Do not add a root `require` plus local `replace`
  for the client module; versioned
  `go install github.com/barzoj-co/llm-test-lab/cmd/llmtestlab@latest` ignores local
  replaces and must build the proxy without the nested client module.
- CLI flags are in `cmd/llmtestlab/main.go`.
- Environment variables and validation are in `internal/lab/config.go`.
- Proxy routing and control endpoints are in `internal/lab/server.go`.
- Session tagging, redaction, SSE parsing, retry parsing, and API family detection are in
  `internal/lab/parse.go`.
- Chat Completions parsing is in `internal/lab/parse_chat.go`.
- Responses parsing is in `internal/lab/parse_responses.go`.
- OTel attributes are in `internal/lab/otel.go` and `internal/semconv/genai`.
- Public Go client APIs are in `pkg/llmtestlab`.

Avoid documenting planned flags, endpoints, or APIs as if they already exist.

## Provider fixtures

Parser behavior should be covered by fixture tests when possible. Existing fixtures live
under `tests/proxy/testdata` and are exercised by `tests/proxy/fixture_proxy_test.go`.
Proxy fixture tests should stay outside `pkg/llmtestlab` and outside the root module's
package tree so the MIT client module does not import AGPL proxy/server code and the
proxy binary remains installable without the nested client module.

Good fixture coverage includes:

- streaming and non-streaming Chat Completions;
- streaming and non-streaming Responses;
- tool-call argument deltas;
- Azure empty-choice content-filter chunks;
- HTTP-200 stream errors;
- structured output;
- usage chunks.

## Security expectations

The proxy forwards provider credentials upstream but records only redacted copies of
secret headers and selected URL query parameters. Raw request and response bodies are
stored in memory. Treat the control API as sensitive:

- keep it bound to loopback by default;
- require `--control-token` / `LLMTESTLAB_CONTROL_TOKEN` for non-loopback binds;
- do not print raw session JSON in public CI logs.

## Pull requests

Keep changes focused. For docs, cross-link the relevant reference pages:

- `docs/proxy.md`
- `docs/providers.md`
- `docs/data-model.md`
- `docs/go-client.md`
- `docs/otel.md`
- `docs/ci.md`
- `docs/troubleshooting.md`

Never merge your own PR unless the repository maintainers explicitly ask you to.
