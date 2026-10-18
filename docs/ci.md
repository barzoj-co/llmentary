# GitHub Actions and CI

The repository's own workflow is in `.github/workflows/ci.yml`.
Triggers are `push` to `main`, `pull_request`, and `workflow_dispatch`.
`workflow_dispatch` does not run live e2e on any ref.

- `codegen-check`: checkout, Go 1.26.x, `make codegen-check` (`spec-verify`,
  `make gen && git diff --exit-code`, `go mod tidy && git diff --exit-code`,
  then `make gen && git diff --exit-code` again). A2 is a temp worktree that
  commits a hand-edit of `gen.go` and expects `make codegen-check` to fail.
  A5 is a temp worktree that adds `createEmbedding` to `operations.txt`, runs
  `make gen`, and expects `go build` to fail with a missing `CreateEmbedding`
  method (no hand-injection into `gen.go`).
- `lint`: checkout, Go 1.26.x, SHA-pinned `golangci/golangci-lint-action` with
  a pinned `golangci-lint` version. Generated `internal/openaiapi/gen.go` is
  excluded in `.golangci.yml`.
- `test`: checkout, Go 1.26.x, `go test` in the root module and
  `tests/proxy` with atomic coverage profiles uploaded to Codecov
  (`use_oidc: false`; this job does not grant `id-token: write`).
  Coverage is informational. GoReleaser snapshots live in the Hygiene
  workflow. OIDC and cosign signing stay in the release workflow.
- `docs-mermaid`: checkout (`actions/checkout` SHA-pinned at v4.4.0), Node 22
  (`actions/setup-node` SHA-pinned at v4.4.0), and
  `@mermaid-js/mermaid-cli@11.12.0` (`mmdc`). Runs
  `bash scripts/check-mermaid.sh --self-test` with a 10-minute job timeout
  (5-minute step timeout). The script requires `python3`, `node`, `npx`, and
  the pinned `mmdc`, extracts CommonMark mermaid fences (backtick or tilde,
  3+ characters, indent up to 3 spaces) from tracked `*.md` files, excluding
  `testdata/mermaid/`, and renders each with `mmdc`. A parse failure is
  reported as `FAIL <file>:<opening-fence-line>`. Extraction or missing-tool
  errors fail the job. Zero extracted blocks while `README.md` still has a
  mermaid fence also fails. An empty or mismatched `mmdc --version` fails
  (it must equal the pinned `MERMAID_CLI_VERSION`). `--self-test` then proves
  a missing `python3` exits non-zero, that empty and mismatched `mmdc --version`
  fail, that `testdata/mermaid/fences.md` (tilde and 4-backtick fences) parses,
  and that `testdata/mermaid/bad.md` (the original README diagram from `main`,
  with unquoted `|llmtestlab.New(t)|`) is rejected.
  Run the same command locally after
  `npm install -g @mermaid-js/mermaid-cli@11.12.0`.
- `e2e`: runs once per change — on `pull_request` (non-fork) and on pushes to
  `main` only, not on both `push` and `pull_request` for PR branches. Uses
  `environment: openai-e2e` (maintainer approval). Checkout, `setup-go`,
  `go mod download`, and `go test -c` run without the key. The presence check
  uses the boolean form `secrets.OPENAI_API_KEY != ''`. Only `Run e2e tests`
  has `OPENAI_API_KEY` in step `env:` and runs `go test -v -tags=e2e ./...` in
  `tests/e2e`. Missing `OPENAI_API_KEY` fails the job.
- `e2e-gate`: always runs, needs `e2e`, and is green only when `e2e` succeeded.
  This is the check that should be required after the repo is public (GitHub
  treats a skipped required `e2e` as passing).
- `perf`: dedicated job, `GOMAXPROCS=2`, runs `go test -tags=perf` in
  `tests/proxy` for validation/lint p99 budgets (I2–I5).
- `codegen-check` also runs `scripts/prove-a6.sh` (editing `relax-overlay.yaml`
  without regenerating the embed must fail).

The e2e tests call the real OpenAI API and skip locally when `OPENAI_API_KEY` is unset.
The CI workflow intentionally fails non-fork e2e runs without the secret so missing
credentials are visible.

## Use the proxy in another Go project

This example downloads a release binary, starts the proxy in the background, and runs
tests against the control API (for example via the
[Go client](https://github.com/barzoj-co/llm-test-lab-go)).

```yaml
name: LLM integration tests

on:
  pull_request:
  push:
    branches: [main]

permissions:
  contents: read

jobs:
  llm-tests:
    runs-on: ubuntu-latest
    if: ${{ github.event_name != 'pull_request' || github.event.pull_request.head.repo.fork != true }}
    env:
      OPENAI_API_KEY: ${{ secrets.OPENAI_API_KEY }}
      LLMTESTLAB_PROXY_URL: http://127.0.0.1:7777
      LLMTESTLAB_CONTROL_URL: http://127.0.0.1:7778
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version: "1.26.x"
          cache: true

      - name: Download llm-test-lab
        env:
          VERSION: v0.1.0
        run: |
          curl -fsSL \
            "https://github.com/barzoj-co/llm-test-lab/releases/download/${VERSION}/llm-test-lab_${VERSION}_linux_amd64.tar.gz" \
            -o llm-test-lab.tar.gz
          tar -xzf llm-test-lab.tar.gz
          chmod +x llmtestlab

      - name: Start llm-test-lab proxy
        run: |
          ./llmtestlab \
            --listen 127.0.0.1:7777 \
            --control 127.0.0.1:7778 \
            --upstream openai=https://api.openai.com/v1 \
            >llmtestlab.log 2>&1 &
          echo $! > llmtestlab.pid
          for i in $(seq 1 50); do
            curl -fsS http://127.0.0.1:7778/healthz && exit 0
            sleep 0.1
          done
          cat llmtestlab.log
          exit 1

      - name: Run proxy/root tests
        run: go test ./...

      - name: Run proxy integration tests
        run: go test ./...
        working-directory: tests/proxy
```

Replace `VERSION` with a published release tag. Release archives are built by
the reusable GoReleaser workflow for Linux, macOS, and Windows on amd64/arm64
when release-please tags `main`, or when a `v*` tag is pushed.

## Use `go install` instead of a release binary

For projects that are comfortable building from source during CI:

```yaml
- name: Install llm-test-lab
  run: go install github.com/barzoj-co/llm-test-lab/cmd/llmtestlab@latest

- name: Start llm-test-lab proxy
  run: |
    llmtestlab \
      --listen 127.0.0.1:7777 \
      --control 127.0.0.1:7778 \
      --upstream openai=https://api.openai.com/v1 \
      >llmtestlab.log 2>&1 &
    for i in $(seq 1 50); do
      curl -fsS http://127.0.0.1:7778/healthz && exit 0
      sleep 0.1
    done
    cat llmtestlab.log
    exit 1
```

## E2E tests with a real key

The repository's e2e tests are behind the `e2e` build tag:

```bash
go test ./...
(cd tests/proxy && go test ./...)
(cd tests/e2e && OPENAI_API_KEY=sk-... go test -v -tags=e2e ./...)
```

Optional:

```bash
cd tests/e2e
OPENAI_E2E_MODEL=gpt-4.1-mini OPENAI_API_KEY=sk-... go test -v -tags=e2e ./...
```

The tests start an in-process proxy and control server with `httptest`, call OpenAI Chat
Completions and Responses, then assert against the recorded session.

## CI secrets and the e2e gate

`OPENAI_API_KEY` is scoped to the `Run e2e tests` step of the `e2e` job. It is
not set at workflow or job level, and it is not present on checkout, Go setup,
module download, or compile. `scripts/check-secret-scope.sh` enforces that
placement on every workflow.

`e2e-gate` is a separate job (`if: always()`, `needs: [e2e]`) that runs
`scripts/e2e-gate.sh`. It exits 0 only when `needs.e2e.result` is `success`.
On a fork PR, `e2e` is skipped and the gate fails with
`live e2e did not run; a maintainer must run it`.

The live `e2e` job is the only job that sets `environment: openai-e2e` and the
only job that may receive `OPENAI_API_KEY`. Unit, fixture, and perf jobs do
not reference that environment, so they run without approval.
`scripts/check-secret-scope.sh` enforces both rules.

Each non-fork PR (and each push to `main`) waits for a maintainer to approve
the `openai-e2e` deployment before the live tests start. Denis configures
required reviewers on that environment. If approval is rejected or times out,
`e2e` is `failure`/`cancelled` and `e2e-gate` goes red. That is intended.

To run live e2e for a fork PR, a maintainer reviews the diff and pushes the
same commits to a same-repo branch, then approves the `openai-e2e`
deployment there. Do not use `pull_request_target` to give a fork the key.

Known limitation: on `pull_request`, GitHub runs the workflow and scripts from
the PR merge commit, so a PR that edits `ci.yml` or `scripts/e2e-gate.sh` can
make the gate pass. The gate still catches the normal fork case (e2e skipped,
nobody tampered with CI). Tampering is caught by review of every `.github/**`
and `scripts/**` diff. Never "fix" this with `pull_request_target`.

## Fork safety

Do not expose provider secrets to untrusted fork PRs. The repository workflow uses:

```yaml
if: ${{ (github.event_name == 'pull_request' && github.event.pull_request.head.repo.fork != true) || (github.event_name == 'push' && github.ref == 'refs/heads/main') }}
```

on the e2e job so it runs once per change (non-fork PRs and pushes to `main`) and
fork pull requests do not receive `OPENAI_API_KEY`.

For public repositories, keep this split:

- run unit tests and fixture tests on all PRs;
- run real-provider e2e tests only on trusted branches, pushes, or non-fork PRs with
  secrets available.

## Control API in CI

Bind the control API to loopback in CI unless you need remote access:

```bash
--control 127.0.0.1:7778
```

If you bind to `0.0.0.0` or another non-loopback address, set a token:

```bash
llmtestlab \
  --control 0.0.0.0:7778 \
  --control-token "$LLMTESTLAB_CONTROL_TOKEN" \
  --upstream openai=https://api.openai.com/v1
```

Then configure tests with `LLMTESTLAB_CONTROL_TOKEN`. The
[Go client](https://github.com/barzoj-co/llm-test-lab-go) reads that variable by default.
