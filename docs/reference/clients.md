# Client APIs

Each client lives in its own repository and is documented there. The proxy,
its control API and the trace data model are documented on this site.

| Client | Install | API reference |
|---|---|---|
| Go `0.5.4` | `go get github.com/barzoj-co/llmentary-go` | [go-client.md](https://github.com/barzoj-co/llmentary-go/blob/main/docs/go-client.md), [pkg.go.dev](https://pkg.go.dev/github.com/barzoj-co/llmentary-go) |
| Python `0.7.3` | `pip install "llmentary[pytest]"` | [api.md](https://github.com/barzoj-co/llmentary-python/blob/main/docs/api.md), [pytest plugin](https://github.com/barzoj-co/llmentary-python/blob/main/docs/pytest.md) |
| TypeScript / JS `0.4.8` | `npm install --save-dev llmentary` | [api.md](https://github.com/barzoj-co/llmentary-js/blob/main/docs/api.md), [assertions guide](https://github.com/barzoj-co/llmentary-js/blob/main/docs/assertions-guide.md) |

All three clients pin proxy `v0.5.3`.

## Naming across languages

| Concept | Go | Python | TypeScript |
|---|---|---|---|
| New session | `llmentary.New(t)` | `llmentary` fixture | `llmentary()` |
| Header-based base URL | `lm.OpenAIBaseURL("")` + `lm.HTTPClient()` | `llmentary.openai_base_url` + `llmentary.headers` | `lm.openAI()` |
| Path-prefix base URL | `lm.PathBaseURL("")` | `llmentary.path_base_url()` | `lm.pathBaseURL()` |
| Assert after quiet | `lm.Expect(ctx)` | `llmentary.expect()` | `await lm.expect()` |
| Assert zero calls | `lm.ExpectNoCalls(ctx)` | `llmentary.expect_no_calls()` | `await lm.expectNoCalls()` |

The wire semantics shared by all clients for spec assertions are pinned in the
[Client assertion contract](../client-contract.md). The control API is described
by [`api/control/openapi.yaml`](https://github.com/barzoj-co/llmentary/blob/main/api/control/openapi.yaml).
