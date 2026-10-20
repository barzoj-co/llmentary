#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Require README badge URLs, user-facing structure, and client API snippets.
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

if ! python3 - "$readme" "$canonical" "${root}" <<'PY'; then
import pathlib
import re
import sys

readme_path = pathlib.Path(sys.argv[1])
block = pathlib.Path(sys.argv[2]).read_text(encoding="utf-8")
root = pathlib.Path(sys.argv[3])
readme = readme_path.read_text(encoding="utf-8")
failed = False

if block not in readme:
    print("README.md is missing the canonical ltl-repos cross-link block", file=sys.stderr)
    failed = True

headings = [line.strip() for line in readme.splitlines() if re.match(r"^#{1,6} ", line)]
want = [
    "# llmentary",
    "## Install",
    "## Quickstart",
    "## Assertions",
    "## Documentation",
    "## llmentary repositories",
    "## License",
]
if headings != want:
    print("README.md headings must be exactly:", file=sys.stderr)
    for h in want:
        print(f"  {h}", file=sys.stderr)
    print("got:", file=sys.stderr)
    for h in headings:
        print(f"  {h}", file=sys.stderr)
    failed = True

required_snippets = (
    "lm := llmentary.New(t)",
    'import { llmentary } from "llmentary/vitest"',
    'const { llmentary } = require("llmentary/jest")',
    "def test_weather(llmentary):",
    "const lm = llmentary()",
)
for snippet in required_snippets:
    if snippet not in readme:
        print(f"README.md is missing client API snippet: {snippet}", file=sys.stderr)
        failed = True

forbidden_readme = (
    "llm-test-lab",
    "Renamed from",
    "lab :=",
    "LLMTESTLAB",
    "cosign verify-blob",
    "CI secrets",
    "private repo",
    "private repositories",
)
for needle in forbidden_readme:
    if needle in readme:
        print(f"README.md must not contain {needle!r}", file=sys.stderr)
        failed = True

# Docs/examples must use lm, not leftover lab client naming.
lab_assign = re.compile(r"\blab\s*:=")
lab_recv = re.compile(r"\blab\.(Expect|HTTPClient|OpenAIBaseURL|Header|Wait|Check|New)\b")
legacy = re.compile(r"llm-test-lab|WithLabControlToken|test-lab|\btestlab\b", re.I)
skip_parts = {"CHANGELOG.md", "testdata"}
for path in sorted(root.rglob("*.md")):
    rel = path.relative_to(root).as_posix()
    if any(part in skip_parts or part == "testdata" for part in path.relative_to(root).parts):
        continue
    if rel == "CHANGELOG.md":
        continue
    text = path.read_text(encoding="utf-8")
    for i, line in enumerate(text.splitlines(), 1):
        if "internal/proxy" in line:
            continue
        if lab_assign.search(line) or lab_recv.search(line) or legacy.search(line):
            print(f"{rel}:{i}: leftover lab/testlab naming: {line.strip()}", file=sys.stderr)
            failed = True

sys.exit(1 if failed else 0)
PY
  failed=1
fi

if [[ "$failed" -ne 0 ]]; then
  exit 1
fi
echo "check-readme: badges, structure, and client API snippets ok"
