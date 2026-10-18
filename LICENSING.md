# Licensing

Only the Go proxy binary is AGPL-3.0. `pkg/llmtestlab` and the separate Python/JS
client repos are MIT.

- Go proxy binary (`cmd/llmtestlab` and the proxy/server implementation it
  ships): AGPL-3.0, covered by the root [LICENSE](LICENSE).
- Go client library (`pkg/llmtestlab`): MIT, covered by
  [`pkg/llmtestlab/LICENSE`](pkg/llmtestlab/LICENSE).
- Separate Python and JS client repositories (`llm-test-lab-python`,
  `llm-test-lab-js`): MIT.

Using the proxy in your test pipeline does not affect your application's
license. Your app talks to the proxy over HTTP. The MIT client libraries are
importable without linking the AGPL proxy/server code.
