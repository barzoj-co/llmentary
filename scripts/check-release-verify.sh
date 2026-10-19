#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# README.md and SECURITY.md must share one fail-closed cosign verify block.
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: scripts/check-release-verify.sh [--self-test]

Require README.md and SECURITY.md to document the same release-verify
commands, including set -euo pipefail, the pinned Fulcio identity,
--certificate-oidc-issuer, and --certificate-github-workflow-repository.

  --self-test  accept scripts/testdata/release-verify/good and reject each bad fixture
EOF
}

SELF_TEST=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --self-test) SELF_TEST=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

root="$(git rev-parse --show-toplevel)"
cd "$root"

# Compare verify blocks in two markdown files. Exit 1 and print errors on stderr.
scan_pair() {
  local readme="$1"
  local security="$2"
  python3 - "$readme" "$security" <<'PY'
from __future__ import annotations

import pathlib
import re
import sys

IDENTITY = (
    "--certificate-identity-regexp "
    "'^https://github.com/barzoj-co/llmentary/\\.github/workflows/"
    "goreleaser\\.yml@refs/(heads/main|tags/v[0-9]+\\.[0-9]+\\.[0-9]+.*)$'"
)
REQUIRED = (
    "set -euo pipefail",
    IDENTITY,
    "--certificate-oidc-issuer https://token.actions.githubusercontent.com",
    "--certificate-github-workflow-repository barzoj-co/llmentary",
)


def extract(path: pathlib.Path) -> str | None:
    text = path.read_text(encoding="utf-8")
    for m in re.finditer(r"```bash\n(.*?)```", text, re.S):
        block = m.group(1)
        if "cosign verify-blob" in block:
            return block
    return None


readme = pathlib.Path(sys.argv[1])
security = pathlib.Path(sys.argv[2])
errors: list[str] = []
try:
    readme_block = extract(readme)
except OSError as exc:
    print(f"{readme}:1: cannot read: {exc}", file=sys.stderr)
    sys.exit(1)
try:
    security_block = extract(security)
except OSError as exc:
    print(f"{security}:1: cannot read: {exc}", file=sys.stderr)
    sys.exit(1)

if readme_block is None:
    errors.append(f"{readme}:1: missing bash verify block with cosign verify-blob")
if security_block is None:
    errors.append(f"{security}:1: missing bash verify block with cosign verify-blob")
if readme_block is not None and security_block is not None and readme_block != security_block:
    errors.append(f"{readme}:1: verify block differs from {security}")

for path, block in ((readme, readme_block), (security, security_block)):
    if block is None:
        continue
    for req in REQUIRED:
        if req not in block:
            errors.append(f"{path}:1: verify block missing {req}")

if errors:
    print("release-verify violations:", file=sys.stderr)
    for err in errors:
        print(err, file=sys.stderr)
    sys.exit(1)
print(f"check-release-verify: {readme} and {security} match")
PY
}

if [[ "$SELF_TEST" -eq 0 ]]; then
  scan_pair README.md SECURITY.md
  exit 0
fi

scan_pair README.md SECURITY.md

good_dir="scripts/testdata/release-verify/good"
if [[ ! -f "${good_dir}/README.md" || ! -f "${good_dir}/SECURITY.md" ]]; then
  echo "self-test fixtures missing under scripts/testdata/release-verify/good/" >&2
  exit 1
fi
if ! scan_pair "${good_dir}/README.md" "${good_dir}/SECURITY.md"; then
  echo "self-test: good fixtures were rejected" >&2
  exit 1
fi
echo "self-test: good fixtures accepted"

self_failed=0
for case_dir in scripts/testdata/release-verify/bad-*; do
  [[ -d "$case_dir" ]] || continue
  status=0
  out="$(scan_pair "${case_dir}/README.md" "${case_dir}/SECURITY.md" 2>&1)" || status=$?
  if [[ "$status" -eq 0 ]]; then
    echo "self-test: ${case_dir} exited 0 (expected nonzero)" >&2
    self_failed=1
    continue
  fi
  if [[ -z "$out" ]]; then
    echo "self-test: ${case_dir} rejected without a message" >&2
    self_failed=1
    continue
  fi
  echo "self-test: ${case_dir} rejected:"
  echo "$out"
done
if [[ "$self_failed" -ne 0 ]]; then
  exit 1
fi
echo "check-release-verify: self-test passed"
