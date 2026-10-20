#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# A5 proof: add createEmbedding to operations.txt in a temp worktree, run
# `make gen` (no hand-edits of gen.go), and assert `go build` fails because
# *openAIHandlers is missing CreateEmbedding on StrictServerInterface.
set -euo pipefail
root="$(git rev-parse --show-toplevel)"
cd "$root"
tmp="$(mktemp -d "${TMPDIR:-/tmp}/a5-proof.XXXXXX")"
errfile=""
cleanup() {
  git -C "$root" worktree remove --force "$tmp" >/dev/null 2>&1 || true
  rm -rf "$tmp"
  rm -f "$errfile"
}
trap cleanup EXIT
git worktree add --detach "$tmp" HEAD
cd "$tmp"
if grep -qx 'createEmbedding' api/openai/operations.txt; then
  echo "A5 proof failed: createEmbedding is already listed; this proof grows the interface" >&2
  exit 1
fi
printf '\ncreateEmbedding\n' >> api/openai/operations.txt
make gen
errfile="$tmp/a5-build.err"
if go build -o "$tmp/llmentary-a5-proof" ./internal/proxy 2>"$errfile"; then
  echo "A5 proof failed: build succeeded after adding createEmbedding" >&2
  exit 1
fi
want='does not implement openaiapi.StrictServerInterface (missing method CreateEmbedding)'
if ! grep -F -- "$want" "$errfile" >/dev/null; then
  echo "A5 proof failed: expected exact missing-method compile error:" >&2
  echo "  $want" >&2
  echo "got:" >&2
  cat "$errfile" >&2
  exit 1
fi
echo "A5 proof: make gen of createEmbedding succeeds; go build fails with missing CreateEmbedding"
echo "$want"
