# Negative fixture (excluded from the docs-mermaid scan)

Original `README.md` "How it works" diagram from `main` (`a2c68e7`).
Unquoted parentheses in `|llmentary.New(t)|` make GitHub's mermaid parser
fail with `Parse error on line 2 ... got PS`. `scripts/check-mermaid.sh --self-test`
must reject this block.

```mermaid
flowchart LR
    T[Go test] -->|llmentary.New(t)| L[Session id]
    L -->|X-Test-Session or baggage| SDK[LLM SDK / app]
    SDK -->|OpenAI-compatible HTTP| P[llmentary proxy]
    P -->|unchanged request body and auth| U[Upstream provider]
    U -->|unchanged response / SSE stream| P
    P --> S[(in-memory session store)]
    P --> O[(in-memory OTLP spans)]
    T -->|GET /sessions/{id}/wait| S
    T --> A[Go assertions]
    T -->|GET /sessions/{id}/otlp| O
```
