#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Fail if a pull_request / pull_request_target workflow grants id-token: write.
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: scripts/check-pr-oidc.sh [--self-test]

Scan .github/workflows/*.y*ml. A workflow triggered by pull_request or
pull_request_target must not grant id-token: write (top-level, write-all,
or any job). OIDC minting belongs in the release workflow only.

  --self-test  also accept scripts/testdata/pr-oidc/good and reject each bad fixture
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

if ! python3 -c "import yaml" >/dev/null 2>&1; then
  echo "PyYAML is required: python3 -m pip install pyyaml" >&2
  exit 1
fi

# Print "file:line: ..." for each violation. Exit 1 if any.
scan_files() {
  python3 - "$@" <<'PY'
from __future__ import annotations

import pathlib
import sys

import yaml

PR_EVENTS = {"pull_request", "pull_request_target"}


def load(path: pathlib.Path):
    with path.open(encoding="utf-8") as fh:
        return yaml.safe_load(fh)


def trigger_events(on) -> set[str]:
    if on is None:
        return set()
    if isinstance(on, str):
        return {on}
    if isinstance(on, list):
        events: set[str] = set()
        for item in on:
            events |= trigger_events(item)
        return events
    if isinstance(on, dict):
        return {str(k) for k in on}
    return set()


def grants_id_token_write(perms) -> bool:
    if perms == "write-all":
        return True
    if isinstance(perms, dict) and str(perms.get("id-token", "")).lower() == "write":
        return True
    return False


def line_of(path: pathlib.Path, needle: str) -> int:
    try:
        for i, line in enumerate(path.read_text(encoding="utf-8").splitlines(), start=1):
            if needle in line:
                return i
    except OSError:
        return 0
    return 0


offending = 0
for raw in sys.argv[1:]:
    path = pathlib.Path(raw)
    try:
        doc = load(path)
    except (OSError, yaml.YAMLError) as exc:
        print(f"{path}:1: cannot parse: {exc}")
        offending += 1
        continue
    if not isinstance(doc, dict):
        print(f"{path}:1: workflow must be a mapping")
        offending += 1
        continue
    on = doc.get("on")
    if on is None:
        on = doc.get(True)
    events = trigger_events(on)
    if not (events & PR_EVENTS):
        continue
    if grants_id_token_write(doc.get("permissions")):
        line = line_of(path, "id-token:") or line_of(path, "write-all") or 1
        print(f"{path}:{line}: top-level grants id-token: write on a pull_request/pull_request_target workflow")
        offending += 1
    jobs = doc.get("jobs")
    if not isinstance(jobs, dict):
        continue
    for job_id, job in jobs.items():
        if not isinstance(job, dict):
            continue
        if grants_id_token_write(job.get("permissions")):
            line = line_of(path, "id-token:") or line_of(path, "write-all") or 1
            print(
                f"{path}:{line}: job {job_id} grants id-token: write on a pull_request/pull_request_target workflow"
            )
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
}

mapfile -t files < <(collect_repo_files | sort)
if [[ "${#files[@]}" -eq 0 ]]; then
  echo "no workflow files under .github/workflows/" >&2
  exit 1
fi
if scan_files "${files[@]}"; then
  echo "check-pr-oidc: ${#files[@]} workflow(s) ok"
else
  echo "check-pr-oidc: pull_request/pull_request_target workflow grants id-token: write" >&2
  exit 1
fi

if [[ "$SELF_TEST" -eq 0 ]]; then
  exit 0
fi

good_dir="scripts/testdata/pr-oidc/good"
bad_dir="scripts/testdata/pr-oidc/bad"
if [[ ! -d "$good_dir" || ! -d "$bad_dir" ]]; then
  echo "self-test fixtures missing under scripts/testdata/pr-oidc/" >&2
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
  if ! grep -q "id-token: write" <<<"$out"; then
    echo "self-test: ${f} rejected without mentioning id-token: write:" >&2
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
echo "check-pr-oidc: self-test passed"
