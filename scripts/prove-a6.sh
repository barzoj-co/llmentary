#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# A6 proof: in a temp worktree, edit relax-overlay.yaml without regenerating
# the embed; `make codegen-check` must fail because `make gen` rewrites the embed.
set -euo pipefail
root="$(git rev-parse --show-toplevel)"
cd "$root"
tmp="$(mktemp -d "${TMPDIR:-/tmp}/a6-proof.XXXXXX")"
out="$(mktemp "${TMPDIR:-/tmp}/a6-codegen-check.XXXXXX")"
cleanup() {
  git -C "$root" worktree remove --force "$tmp" >/dev/null 2>&1 || true
  rm -rf "$tmp" "$out"
}
trap cleanup EXIT
git worktree add --detach "$tmp" HEAD
cd "$tmp"
printf '\n# a6-proof-stale-overlay\n' >> api/openai/relax-overlay.yaml
git add api/openai/relax-overlay.yaml
git -c user.email=a6-proof@llm-test-lab.invalid -c user.name=a6-proof \
  commit -m "a6-proof: stale relax-overlay.yaml" >/dev/null
if make codegen-check >"$out" 2>&1; then
  echo "A6 proof failed: make codegen-check succeeded after a committed overlay edit without regen" >&2
  cat "$out" >&2
  exit 1
fi
if ! grep -q 'relax-overlay.yaml' "$out" && ! grep -q 'specvalidate/embed' "$out"; then
  echo "A6 proof failed: codegen-check failed, but not because the overlay/embed drifted:" >&2
  cat "$out" >&2
  exit 1
fi
echo "A6 proof: committed overlay edit without regen is rejected by make codegen-check"
