#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Merge unit + proxy fixture coverprofiles and fail below the stored threshold.
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: scripts/coverage-gate.sh [--self-test] [unit.out proxy.out]

Merge Go coverprofiles (max hit-count per block), exclude the same paths as
codecov.yml (internal/openaiapi/gen.go, tools/**), and fail if the floored
statement percent is below scripts/coverage-threshold.

Release-please PRs (title chore(main): release … or branch
release-please--branches--main) require 90 regardless of that file.

  --self-test  run the built-in case table
EOF
}

RELEASE_MIN=90

is_release_please() {
  local title="${PR_TITLE-}"
  local ref="${HEAD_REF-}"
  if [[ -z "$ref" ]]; then
    ref="${GITHUB_HEAD_REF-}"
  fi
  if [[ -z "$ref" ]]; then
    ref="${GITHUB_REF_NAME-}"
  fi
  if [[ "$title" == "chore(main): release"* ]]; then
    return 0
  fi
  if [[ "$ref" == release-please--branches--* ]]; then
    return 0
  fi
  return 1
}

read_threshold() {
  local file="$1"
  if [[ ! -f "$file" ]]; then
    echo "::error::coverage threshold file missing: $file" >&2
    return 1
  fi
  local raw
  raw="$(grep -E '^[0-9]+$' "$file" | head -n 1 || true)"
  if [[ -z "$raw" ]]; then
    echo "::error::coverage threshold file has no integer line: $file" >&2
    return 1
  fi
  echo "$raw"
}

# Prints: covered total percent_unfloored floor
merge_and_score() {
  python3 - "$@" <<'PY'
import pathlib
import sys

def ignored(path: str) -> bool:
    norm = path.replace("\\", "/")
    if norm.endswith("/internal/openaiapi/gen.go") or norm.endswith("internal/openaiapi/gen.go"):
        return True
    if "/tools/" in norm or norm.startswith("tools/"):
        return True
    return False

def parse(path: str) -> dict[str, tuple[int, int]]:
    blocks: dict[str, tuple[int, int]] = {}
    text = pathlib.Path(path).read_text(encoding="utf-8")
    for i, line in enumerate(text.splitlines(), start=1):
        line = line.strip()
        if not line or line.startswith("mode:"):
            continue
        parts = line.rsplit(" ", 2)
        if len(parts) != 3:
            raise SystemExit(f"{path}:{i}: bad coverprofile line")
        loc, stmts_s, count_s = parts
        if ignored(loc.split(":")[0]):
            continue
        stmts = int(stmts_s)
        count = int(count_s)
        prev = blocks.get(loc)
        if prev is None:
            blocks[loc] = (count, stmts)
        else:
            blocks[loc] = (max(prev[0], count), stmts)
    return blocks

merged: dict[str, tuple[int, int]] = {}
for path in sys.argv[1:]:
    for loc, (count, stmts) in parse(path).items():
        prev = merged.get(loc)
        if prev is None:
            merged[loc] = (count, stmts)
        else:
            merged[loc] = (max(prev[0], count), stmts)

covered = total = 0
for count, stmts in merged.values():
    total += stmts
    if count > 0:
        covered += stmts
if total == 0:
    print("0 0 0.0000 0")
    raise SystemExit(0)
pct = 100.0 * covered / total
print(f"{covered} {total} {pct:.4f} {int(pct)}")
PY
}

gate() {
  local unit="$1"
  local proxy="$2"
  local root
  root="$(git rev-parse --show-toplevel)"
  local thresh_file="$root/scripts/coverage-threshold"
  local min
  min="$(read_threshold "$thresh_file")"
  local required="$min"
  if is_release_please; then
    required="$RELEASE_MIN"
  fi
  if [[ ! -f "$unit" ]]; then
    echo "::error::unit coverprofile missing: $unit" >&2
    return 1
  fi
  if [[ ! -f "$proxy" ]]; then
    echo "::error::proxy coverprofile missing: $proxy" >&2
    return 1
  fi
  local score
  score="$(merge_and_score "$unit" "$proxy")"
  local covered total pct floor
  read -r covered total pct floor <<<"$score"
  echo "coverage-gate: merged ${covered}/${total} statements (${pct}%, floor ${floor})"
  echo "coverage-gate: required ${required} (threshold file ${min}; release-please ${RELEASE_MIN})"
  if (( floor < required )); then
    echo "::error::merged coverage ${pct}% (floor ${floor}) is below ${required}" >&2
    return 1
  fi
  return 0
}

self_test() {
  local tmp
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' RETURN
  local failed=0

  cat >"$tmp/unit.out" <<'EOF'
mode: atomic
github.com/barzoj-co/llmentary/internal/proxy/a.go:1.1,2.1 10 1
github.com/barzoj-co/llmentary/internal/openaiapi/gen.go:1.1,2.1 1000 1
github.com/barzoj-co/llmentary/tools/gencode/main.go:1.1,2.1 50 1
EOF
  cat >"$tmp/proxy.out" <<'EOF'
mode: atomic
github.com/barzoj-co/llmentary/internal/proxy/a.go:1.1,2.1 10 0
github.com/barzoj-co/llmentary/internal/proxy/b.go:1.1,2.1 10 2
EOF

  local score
  score="$(merge_and_score "$tmp/unit.out" "$tmp/proxy.out")"
  local covered total pct floor
  read -r covered total pct floor <<<"$score"
  echo "self-test: merge ${covered}/${total} ${pct}% floor=${floor}"
  if [[ "$covered" != "20" || "$total" != "20" || "$floor" != "100" ]]; then
    echo "self-test: merge: expected 20/20 floor 100, got ${score}" >&2
    failed=1
  fi

  cat >"$tmp/low-unit.out" <<'EOF'
mode: atomic
github.com/barzoj-co/llmentary/internal/proxy/a.go:1.1,2.1 10 1
github.com/barzoj-co/llmentary/internal/proxy/b.go:1.1,2.1 10 0
EOF
  cat >"$tmp/low-proxy.out" <<'EOF'
mode: atomic
github.com/barzoj-co/llmentary/internal/proxy/a.go:1.1,2.1 10 0
github.com/barzoj-co/llmentary/internal/proxy/b.go:1.1,2.1 10 0
EOF
  score="$(merge_and_score "$tmp/low-unit.out" "$tmp/low-proxy.out")"
  read -r covered total pct floor <<<"$score"
  echo "self-test: low merge ${covered}/${total} ${pct}% floor=${floor}"
  if [[ "$covered" != "10" || "$total" != "20" || "$floor" != "50" ]]; then
    echo "self-test: low merge: expected 10/20 floor 50, got ${score}" >&2
    failed=1
  fi

  local root
  root="$(git rev-parse --show-toplevel)"
  local min
  min="$(read_threshold "$root/scripts/coverage-threshold")"
  if [[ ! "$min" =~ ^[0-9]+$ ]] || (( min < 1 || min > 100 )); then
    echo "self-test: threshold file invalid: ${min}" >&2
    failed=1
  fi

  if PR_TITLE="fix: something" HEAD_REF="cursor/coverage-gate-f2b6" is_release_please; then
    echo "self-test: ordinary PR misclassified as release-please" >&2
    failed=1
  fi
  if ! PR_TITLE="chore(main): release 0.5.0" HEAD_REF="release-please--branches--main" is_release_please; then
    echo "self-test: release-please title/branch not detected" >&2
    failed=1
  fi
  if ! PR_TITLE="" HEAD_REF="release-please--branches--main" is_release_please; then
    echo "self-test: release-please branch not detected" >&2
    failed=1
  fi

  if PR_TITLE="fix: x" HEAD_REF="feat" gate "$tmp/low-unit.out" "$tmp/low-proxy.out" >/dev/null; then
    echo "self-test: low coverage should fail the ratchet" >&2
    failed=1
  else
    echo "self-test: low coverage fails the ratchet (ok)"
  fi

  if ! PR_TITLE="fix: x" HEAD_REF="feat" gate "$tmp/unit.out" "$tmp/proxy.out" >/dev/null; then
    echo "self-test: 100% coverage should pass" >&2
    failed=1
  else
    echo "self-test: 100% coverage passes (ok)"
  fi

  if PR_TITLE="chore(main): release 0.5.0" HEAD_REF="release-please--branches--main" \
    gate "$tmp/low-unit.out" "$tmp/low-proxy.out" >/dev/null; then
    echo "self-test: release-please should require 90" >&2
    failed=1
  else
    echo "self-test: release-please 50% fails 90 (ok)"
  fi

  if [[ "$failed" -ne 0 ]]; then
    echo "coverage-gate: self-test failed" >&2
    return 1
  fi
  echo "coverage-gate: self-test passed"
}

case "${1-}" in
  --self-test)
    self_test
    ;;
  -h|--help)
    usage
    ;;
  "")
    echo "usage: scripts/coverage-gate.sh unit.out proxy.out" >&2
    usage >&2
    exit 2
    ;;
  *)
    if [[ $# -ne 2 ]]; then
      echo "expected two coverprofile paths" >&2
      usage >&2
      exit 2
    fi
    gate "$1" "$2"
    ;;
esac
