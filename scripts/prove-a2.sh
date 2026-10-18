#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# A2 proof: in a temp worktree, commit a hand-edit of gen.go;
# `make codegen-check` must fail because regeneration restores the file.
set -euo pipefail
root="$(git rev-parse --show-toplevel)"
cd "$root"
tmp="$(mktemp -d "${TMPDIR:-/tmp}/a2-proof.XXXXXX")"
cleanup() {
  git -C "$root" worktree remove --force "$tmp" >/dev/null 2>&1 || true
  rm -rf "$tmp"
}
trap cleanup EXIT
git worktree add --detach "$tmp" HEAD
cd "$tmp"
printf '\n// hand-edit-should-fail-codegen-check\n' >> internal/openaiapi/gen.go
git add internal/openaiapi/gen.go
git -c user.email=a2-proof@llm-test-lab.invalid -c user.name=a2-proof \
  commit -m "a2-proof: hand-edit gen.go" >/dev/null
if make codegen-check >/tmp/a2-codegen-check.out 2>&1; then
  echo "A2 proof failed: make codegen-check succeeded after a committed hand-edit of gen.go" >&2
  cat /tmp/a2-codegen-check.out >&2
  exit 1
fi
if ! grep -q 'hand-edit-should-fail-codegen-check' /tmp/a2-codegen-check.out && \
   ! grep -q 'internal/openaiapi/gen.go' /tmp/a2-codegen-check.out; then
  echo "A2 proof failed: codegen-check failed, but not because gen.go drifted:" >&2
  cat /tmp/a2-codegen-check.out >&2
  exit 1
fi
echo "A2 proof: committed hand-edit of gen.go is rejected by make codegen-check"
