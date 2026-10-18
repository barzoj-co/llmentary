# Contributing

Thanks for working on `llm-test-lab`.

## Requirements

- Go 1.26.x, matching `go.mod` and the GitHub Actions workflows.
- Standard Go tooling (`go test`, `gofmt`).

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
