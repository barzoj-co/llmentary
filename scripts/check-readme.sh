#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Require README badge URLs and the canonical cross-repo block.
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
readme="${root}/README.md"

if [[ ! -f "$readme" ]]; then
  echo "README.md is missing" >&2
  exit 1
fi

badges=(
  'https://github.com/barzoj-co/llmentary/actions/workflows/ci.yml/badge.svg?branch=main'
  'https://github.com/barzoj-co/llmentary/actions/workflows/ci.yml'
  'https://codecov.io/gh/barzoj-co/llmentary/graph/badge.svg'
  'https://codecov.io/gh/barzoj-co/llmentary'
  'https://api.scorecard.dev/projects/github.com/barzoj-co/llmentary/badge'
  'https://scorecard.dev/viewer/?uri=github.com/barzoj-co/llmentary'
  'https://img.shields.io/github/license/barzoj-co/llmentary'
  'https://img.shields.io/github/v/release/barzoj-co/llmentary?sort=semver'
  'https://github.com/barzoj-co/llmentary/releases'
)

failed=0
for url in "${badges[@]}"; do
  if ! grep -F -q -- "$url" "$readme"; then
    echo "README.md is missing badge URL: ${url}" >&2
    failed=1
  fi
done

canonical="$(mktemp)"
trap 'rm -f "$canonical"' EXIT
cat >"$canonical" <<'EOF'
<!-- ltl-repos:start -->
## llmentary repositories

| Repository | What | License |
|---|---|---|
| [llmentary](https://github.com/barzoj-co/llmentary) | Recording proxy binary (Go) | AGPL-3.0 |
| [llmentary-go](https://github.com/barzoj-co/llmentary-go) | Go client for `go test` | MIT |
| [llmentary-python](https://github.com/barzoj-co/llmentary-python) | Python client and pytest plugin | MIT |
| [llmentary-js](https://github.com/barzoj-co/llmentary-js) | TypeScript/JavaScript client for Vitest and Jest | MIT |
<!-- ltl-repos:end -->
EOF

if ! python3 - "$readme" "$canonical" <<'PY'; then
import pathlib
import sys
readme = pathlib.Path(sys.argv[1]).read_text(encoding="utf-8")
block = pathlib.Path(sys.argv[2]).read_text(encoding="utf-8")
sys.exit(0 if block in readme else 1)
PY
  echo "README.md is missing the canonical ltl-repos cross-link block" >&2
  failed=1
fi

if [[ "$failed" -ne 0 ]]; then
  exit 1
fi
echo "check-readme: badges and cross-link block ok"
