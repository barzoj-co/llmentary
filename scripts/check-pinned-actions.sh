#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Fail unless every GitHub Actions `uses:` is pinned (SHA + version comment).
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: scripts/check-pinned-actions.sh [--self-test]

Scan .github/workflows/*.y*ml and .github/actions/**/action.y*ml.
Each uses: value must be one of:
  owner/repo[/path]@<40 hex> # vX...
  ./local/path
  docker://image@sha256:<64 hex>

  --self-test  accept scripts/testdata/pinned-actions/good and reject each bad fixture
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

# Print "file:line: uses: ..." for every unpinned uses: and return 1 if any.
scan_files() {
  python3 - "$@" <<'PY'
import pathlib
import re
import sys

USES_RE = re.compile(
    r"""^([ \t]*)(?:-\s*)?uses:[ \t]*(?:['"]([^'"]+)['"]|(\S+))[ \t]*(?:#\s*(.*))?\s*$"""
)
SHA_RE = re.compile(r"^[0-9a-f]{40}$")
DOCKER_RE = re.compile(r"^docker://.+@sha256:[0-9a-f]{64}$")
ACTION_RE = re.compile(r"^([^/]+)/([^/@]+)(?:/[^@]+)?@([0-9a-fA-F]+)$")
VERSION_COMMENT_RE = re.compile(r"^v\S+")

offending = 0
for raw in sys.argv[1:]:
    path = pathlib.Path(raw)
    try:
        lines = path.read_text(encoding="utf-8").splitlines()
    except OSError as exc:
        print(f"{path}:1: cannot read: {exc}", file=sys.stderr)
        offending += 1
        continue
    for i, line in enumerate(lines, start=1):
        stripped = line.lstrip()
        if stripped.startswith("#"):
            continue
        m = USES_RE.match(line)
        if not m:
            continue
        value = m.group(2) if m.group(2) is not None else m.group(3)
        comment = (m.group(4) or "").strip()
        if value.startswith("./"):
            continue
        if DOCKER_RE.match(value):
            continue
        am = ACTION_RE.match(value)
        ok = (
            am is not None
            and SHA_RE.match(am.group(3).lower()) is not None
            and VERSION_COMMENT_RE.match(comment) is not None
        )
        if not ok:
            print(f"{path}:{i}: uses: {value}" + (f" # {comment}" if comment else ""))
            offending += 1
sys.exit(1 if offending else 0)
PY
}

collect_repo_files() {
  local f
  shopt -s nullglob
  for f in .github/workflows/*.{yml,yaml}; do
    printf '%s\n' "$f"
  done
  if [[ -d .github/actions ]]; then
    while IFS= read -r -d '' f; do
      printf '%s\n' "$f"
    done < <(find .github/actions -type f \( -name 'action.yml' -o -name 'action.yaml' \) -print0)
  fi
}

if [[ "$SELF_TEST" -eq 0 ]]; then
  mapfile -t files < <(collect_repo_files | sort)
  if [[ "${#files[@]}" -eq 0 ]]; then
    echo "no workflow or action files found" >&2
    exit 1
  fi
  if scan_files "${files[@]}"; then
    echo "pinned-actions: ${#files[@]} file(s) ok"
    exit 0
  fi
  echo "pinned-actions: unpinned uses: values found" >&2
  exit 1
fi

good_dir="scripts/testdata/pinned-actions/good"
bad_dir="scripts/testdata/pinned-actions/bad"
if [[ ! -d "$good_dir" || ! -d "$bad_dir" ]]; then
  echo "self-test fixtures missing under scripts/testdata/pinned-actions/" >&2
  exit 1
fi

mapfile -t good_files < <(find "$good_dir" -type f \( -name '*.yml' -o -name '*.yaml' \) | sort)
if [[ "${#good_files[@]}" -eq 0 ]]; then
  echo "self-test: no good fixtures" >&2
  exit 1
fi
if ! scan_files "${good_files[@]}"; then
  echo "self-test: good fixtures were rejected" >&2
  exit 1
fi
echo "self-test: good fixtures accepted"

mapfile -t bad_files < <(find "$bad_dir" -type f \( -name '*.yml' -o -name '*.yaml' \) | sort)
if [[ "${#bad_files[@]}" -eq 0 ]]; then
  echo "self-test: no bad fixtures" >&2
  exit 1
fi
self_failed=0
for f in "${bad_files[@]}"; do
  out="$(scan_files "$f" 2>&1 || true)"
  if [[ -z "$out" ]]; then
    echo "self-test: ${f} was accepted (expected reject with file:line)" >&2
    self_failed=1
    continue
  fi
  if ! grep -E -q "^${f}:[0-9]+:" <<<"$out"; then
    echo "self-test: ${f} rejected without file:line message:" >&2
    echo "$out" >&2
    self_failed=1
    continue
  fi
  echo "self-test: ${f} rejected:"
  echo "$out"
done
if [[ "$self_failed" -ne 0 ]]; then
  exit 1
fi
echo "pinned-actions: self-test passed"
