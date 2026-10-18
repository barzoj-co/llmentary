# GitHub Actions and CI

The repository's own workflow is in `.github/workflows/ci.yml`:

- `lint`: checkout, Go 1.26.x, `golangci/golangci-lint-action@v8`.
- `test`: checkout, Go 1.26.x, `go test ./...` in the root AGPL proxy module and
  `go test ./...` in the nested MIT Go client module, proxy integration test module,
  and Go example module.
- `e2e`: skipped for fork pull requests; for pushes and non-fork PRs it requires
  `OPENAI_API_KEY` and runs `go test -v -tags=e2e ./...` in `tests/e2e`.

The e2e tests call the real OpenAI API and skip locally when `OPENAI_API_KEY` is unset.
The CI workflow intentionally fails non-fork e2e runs without the secret so missing
credentials are visible.

## Use the proxy in another Go project

This example downloads a release binary, starts the proxy in the background, and runs Go
tests that use `llmtestlab.New(t)`.

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

      - name: Run MIT Go client tests
        run: go test ./...
        working-directory: pkg/llmtestlab

      - name: Run proxy integration tests
        run: go test ./...
        working-directory: tests/proxy
```

Replace `VERSION` with a published release tag. Release archives are built by
`.github/workflows/release.yml` for Linux, macOS, and Windows on amd64/arm64 when a `v*`
tag is pushed.

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
(cd pkg/llmtestlab && go test ./...)
(cd tests/proxy && go test ./...)
(cd examples/go && go test ./...)
(cd tests/e2e && OPENAI_API_KEY=sk-... go test -v -tags=e2e ./...)
```

Optional:

```bash
cd tests/e2e
OPENAI_E2E_MODEL=gpt-4.1-mini OPENAI_API_KEY=sk-... go test -v -tags=e2e ./...
```

The tests start an in-process proxy and control server with `httptest`, call OpenAI Chat
Completions and Responses, then assert against the recorded session through the Go client.

## Fork safety

Do not expose provider secrets to untrusted fork PRs. The repository workflow uses:

```yaml
if: ${{ github.event_name != 'pull_request' || github.event.pull_request.head.repo.fork != true }}
```

on the e2e job so fork pull requests do not receive `OPENAI_API_KEY`.

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

Then configure tests with `LLMTESTLAB_CONTROL_TOKEN`, which the Go client reads by
default.
