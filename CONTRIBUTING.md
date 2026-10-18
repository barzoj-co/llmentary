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
  `ensureDiscriminatorMappings`. The `CreateEmbeddingResponse.object` enum
  is renamed to `EmbeddingObjectType` by `renameResponseObjectEnums` when
  that schema is in the filtered subgraph (an overlay action would fail
  strict apply while embeddings are not generated).
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

Run unit and fixture tests for the proxy modules:

```bash
go test ./...
(cd tests/proxy && go test ./...)
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
`httptest`, call Chat Completions and Responses, and assert on recorded traces.

## CI secrets and the e2e gate

CI scopes `OPENAI_API_KEY` to the `Run e2e tests` step of the live `e2e` job.
That job is the only one that uses `environment: openai-e2e`. Mock, fixture,
and other integration tests do not, so they run without approval.

`e2e-gate` is the always-run check that is green only when that live job
succeeded. Fork PRs skip `e2e` and fail the gate; a maintainer reviews the
diff and pushes it to a same-repo branch so e2e can run. Do not use
`pull_request_target`.

Maintainers approve the `openai-e2e` deployment on each PR (and on each push
to `main`) before live e2e starts. Denis configures the required reviewers on
that environment. See `docs/ci.md`.

On `pull_request`, GitHub evaluates `e2e-gate` from the PR's own workflow
files; review `.github/**` and `scripts/**` diffs. The gate is not a substitute
for that review.

Before sending code changes, format Go files:

```bash
gofmt -w ./cmd ./internal
```

CI also runs `golangci-lint-action@v8`.

## Documentation rule

Document only behavior implemented in the current code. In particular:

- The proxy test modules carry their own local `replace` directives for the root
  proxy module. Versioned
  `go install github.com/barzoj-co/llm-test-lab/cmd/llmtestlab@latest` ignores local
  replaces and must build the proxy without a nested client module.
- CLI flags are in `cmd/llmtestlab/main.go`.
- Environment variables and validation are in `internal/lab/config.go`.
- Proxy routing and control endpoints are in `internal/lab/server.go`.
- Session tagging, redaction, SSE parsing, retry parsing, and API family detection are in
  `internal/lab/parse.go`.
- Chat Completions parsing is in `internal/lab/parse_chat.go`.
- Responses parsing is in `internal/lab/parse_responses.go`.
- OTel attributes are in `internal/lab/otel.go` and `internal/semconv/genai`.
- Public client APIs live in the separate client repositories.

Avoid documenting planned flags, endpoints, or APIs as if they already exist.

## Provider fixtures

Parser behavior should be covered by fixture tests when possible. Existing fixtures live
under `tests/proxy/testdata` and are exercised by `tests/proxy/fixture_proxy_test.go`.
Proxy fixture tests should stay in `tests/proxy` so the proxy binary remains
installable without pulling test-only helpers into the released module graph.

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

## Spec validation overlay and schema subset

`api/openai/relax-overlay.yaml` is the relaxed-profile deviation list. Every
entry needs `id`, `why`, `evidence.live_test` (an existing `tests/e2e` function),
an optional docs URL, and `added` (ISO date). Adding an entry without a live
test fails `TestC4OverlayEvidence`. After editing the overlay or
`api/openai/schema-subset.yaml`, run `make gen` so
`internal/specvalidate/embed/` stays in sync. `codegen-check` fails if the
embed is stale (`scripts/prove-a6.sh`).

`api/openai/schema-subset.yaml` is the single source for OpenAI's documented
JSON Schema keyword subset. Refresh it from:

- https://developers.openai.com/api/docs/guides/structured-outputs#supported-schemas
- https://developers.openai.com/api/docs/guides/function-calling#strict-mode

Bump `retrieved` when the docs change. Do not hard-code keyword lists in Go.

When OpenAI changes live behavior, update `tests/e2e/testdata/live-behavior.json`
and the overlay together. Live tests fail with a message that names those files.
Never auto-update or skip.

## Pull requests

Keep changes focused. For docs, cross-link the relevant reference pages:

- `docs/proxy.md`
- `docs/providers.md`
- `docs/data-model.md`
- `docs/otel.md`
- `docs/ci.md`
- `docs/troubleshooting.md`

Never merge your own PR unless the repository maintainers explicitly ask you to.

PR titles should use [Conventional Commits](https://www.conventionalcommits.org/)
(`feat:`, `fix:`, `docs:`, `ci:`, `chore:`, …). release-please reads the squash-merge
commit title to choose the next version.

## Code of Conduct and security

- [Code of Conduct](CODE_OF_CONDUCT.md)
- [Security policy](SECURITY.md)

## Coverage

CI uploads unit and proxy fixture coverage to Codecov. Project and
patch statuses are informational. Separately, the CI upload step fails
CI only when `vars.CODECOV_REQUIRED == 'true'`.

## Renovate

Renovate opens weekly dependency PRs (`dependencies` label). Patch and digest
updates automerge after required checks pass. `oapi-codegen` updates do not
automerge: run `make gen` and commit `internal/openaiapi/gen.go`. The vendored
OpenAI spec under `api/` is ignored by Renovate; update it with
`make spec-update`.

## Workflow lint

From the repo root:

```bash
# pin + version-comment check (also --self-test)
bash scripts/check-pinned-actions.sh
bash scripts/check-pinned-actions.sh --self-test

# permissions, concurrency, timeouts (requires PyYAML)
python3 -m pip install pyyaml
bash scripts/check-workflow-policy.sh

# no id-token: write on pull_request / pull_request_target workflows
bash scripts/check-pr-oidc.sh --self-test

# secret placement, e2e-gate job shape (requires PyYAML)
bash scripts/check-secret-scope.sh
bash scripts/check-secret-scope.sh --self-test

# e2e-gate case table
bash scripts/e2e-gate.sh --self-test

# release tag must be an ancestor of origin/main
bash scripts/check-ref-on-main.sh --self-test

# README badges and cross-link block
bash scripts/check-readme.sh

# README and SECURITY.md verify commands stay identical and fail-closed
bash scripts/check-release-verify.sh --self-test

# mermaid diagrams (needs @mermaid-js/mermaid-cli@11.12.0)
bash scripts/check-mermaid.sh --self-test
```

CI runs [actionlint](https://github.com/rhysd/actionlint) with shellcheck on
every workflow file.

## Releasing

Merging the release-please PR tags the next version and publishes GitHub
Release notes. GoReleaser then attaches signed archives, checksums, and SBOMs
to that existing release.

`release-please-config.json` currently sets `"release-as": "0.1.0"` so the first
release is v0.1.0 (hygiene commits are `ci:` / `chore:` and would not bump).
Delete that key in the first release-please PR so later versions come from
conventional commits.

In an emergency, push an annotated tag `vX.Y.Z` to `main`'s commit. The
`Release` workflow calls the same reusable GoReleaser job. Do not create a
second changelog on the GitHub Release.
