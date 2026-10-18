#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# A5 proof: growing StrictServerInterface without implementing the new
# method fails `go build` via the compile-time assertion in handler_openai.go.
# This injects createEmbedding into a temporary copy of gen.go so the proof
# does not depend on regenerating the full embeddings subgraph.
set -euo pipefail
root="$(git rev-parse --show-toplevel)"
cd "$root"
gen=internal/openaiapi/gen.go
cp "$gen" "$gen.a5.bak"
trap 'mv "$gen.a5.bak" "$gen"' EXIT
python3 - <<'PY'
from pathlib import Path
path = Path("internal/openaiapi/gen.go")
text = path.read_text()
needle = "\tCreateResponse(ctx context.Context, request CreateResponseRequestObject) (CreateResponseResponseObject, error)\n}"
insert = "\tCreateResponse(ctx context.Context, request CreateResponseRequestObject) (CreateResponseResponseObject, error)\n\tCreateEmbedding(ctx context.Context, request CreateChatCompletionRequestObject) (CreateChatCompletionResponseObject, error)\n}"
if needle not in text:
    raise SystemExit("could not find StrictServerInterface CreateResponse method")
path.write_text(text.replace(needle, insert, 1))
PY
if go build -o /tmp/llmtestlab-a5-proof ./internal/lab 2>/tmp/a5-build.err; then
  echo "A5 proof failed: build succeeded after adding CreateEmbedding" >&2
  exit 1
fi
if ! grep -Eqi 'does not implement|missing method|CreateEmbedding' /tmp/a5-build.err; then
  echo "A5 proof failed: build error did not mention missing CreateEmbedding" >&2
  cat /tmp/a5-build.err >&2
  exit 1
fi
echo "A5 proof: go build fails with missing CreateEmbedding after interface growth"
