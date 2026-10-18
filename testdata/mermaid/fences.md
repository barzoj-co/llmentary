# Alternate CommonMark mermaid fences (excluded from the docs-mermaid scan)

Used by `scripts/check-mermaid.sh --self-test` to prove tilde fences and
4-backtick fences are extracted and validated.

~~~~mermaid
flowchart LR
    A[Start] --> B[End]
~~~~

````mermaid
flowchart LR
    C[Left] --> D[Right]
````
