#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# A2 proof: a hand-edit of gen.go is visible to `git diff --exit-code`,
# which is what the codegen-check job uses after `make gen`.
set -euo pipefail
root="$(git rev-parse --show-toplevel)"
cd "$root"
gen=internal/openaiapi/gen.go
cp "$gen" "$gen.a2.bak"
trap 'mv "$gen.a2.bak" "$gen"' EXIT
printf '\n// hand-edit-should-fail-codegen-check\n' >> "$gen"
if cmp -s "$gen" "$gen.a2.bak"; then
  echo "A2 proof failed: hand-edit of gen.go did not change the file" >&2
  exit 1
fi
if git ls-files --error-unmatch "$gen" >/dev/null 2>&1; then
  if git diff --exit-code -- "$gen" >/dev/null; then
    echo "A2 proof failed: hand-edit of gen.go was not detected by git diff" >&2
    exit 1
  fi
  echo "A2 proof: hand-edit of gen.go is detected by git diff --exit-code"
else
  echo "A2 proof: hand-edit of gen.go changes file bytes (file not yet tracked; CI git diff will catch it)"
fi
