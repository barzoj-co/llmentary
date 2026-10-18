#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Fail unless the given ref's commit is an ancestor of origin/main.
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: scripts/check-ref-on-main.sh [<ref>]
       scripts/check-ref-on-main.sh --self-test

Resolve <ref>^{commit} (default HEAD) and require it to be an ancestor of
origin/main. Fail closed if refs/remotes/origin/main is missing.

  --self-test  build throwaway repos and assert the case table
EOF
}

check_ref() {
  local ref="${1:-HEAD}"
  local commit short

  if ! commit="$(git rev-parse --verify "${ref}^{commit}" 2>/dev/null)"; then
    echo "::error::cannot resolve ${ref} to a commit"
    return 1
  fi

  if ! git show-ref --verify --quiet refs/remotes/origin/main; then
    echo "::error::origin/main not available; checkout needs fetch-depth: 0"
    return 1
  fi

  if ! git merge-base --is-ancestor "$commit" origin/main; then
    short="$(git rev-parse --short "$commit")"
    echo "::error::${ref} (${short}) is not on main; refusing to release"
    return 1
  fi

  echo "$commit"
}

self_test() {
  local script work origin status out failed=0
  script="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/$(basename "${BASH_SOURCE[0]}")"
  tmp="$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/ref-on-main.XXXXXX")"
  # Expand $tmp now so EXIT cleanup still works after this function returns.
  trap "rm -rf $(printf '%q' "$tmp")" EXIT

  origin="$tmp/origin.git"
  work="$tmp/work"
  git -c init.defaultBranch=main init --bare "$origin" >/dev/null
  git clone "$origin" "$work" >/dev/null
  (
    cd "$work"
    git config user.email "sec-proxy-self-test@example.com"
    git config user.name "sec-proxy-self-test"
    git checkout -B main >/dev/null
    echo first >file
    git add file
    git commit -m first >/dev/null
    git push -u origin main >/dev/null
    echo second >>file
    git commit -am second >/dev/null
    git push origin main >/dev/null
  )

  run_case() {
    local name="$1" want="$2" want_msg="${3-}"
    shift 3 || shift 2
    status=0
    out="$(cd "$work" && "$script" "$@" 2>&1)" || status=$?
    echo "self-test: ${name} → exit ${status} (expected ${want})"
    if [[ -n "$out" ]]; then
      echo "$out"
    fi
    if [[ "$status" -ne "$want" ]]; then
      echo "self-test: ${name}: expected exit ${want}, got ${status}" >&2
      failed=1
    fi
    if [[ -n "$want_msg" ]] && ! grep -F -q -- "$want_msg" <<<"$out"; then
      echo "self-test: ${name}: output did not contain ${want_msg@Q}" >&2
      failed=1
    fi
  }

  local ancestor tip
  ancestor="$(cd "$work" && git rev-parse HEAD~1)"
  tip="$(cd "$work" && git rev-parse HEAD)"

  run_case "commit-on-main" 0 "" "$ancestor"
  run_case "current-main-tip" 0 "" HEAD

  (
    cd "$work"
    git tag -a v1.2.3 -m "annotated on main"
  )
  run_case "annotated-tag-on-main" 0 "" v1.2.3

  (
    cd "$work"
    git checkout -b side >/dev/null
    echo side >>file
    git commit -am side >/dev/null
    git tag side-tag
    git checkout main >/dev/null
  )
  run_case "lightweight-tag-not-on-main" 1 "is not on main; refusing to release" side-tag

  local doomed
  doomed="$(
    cd "$work"
    git checkout -b doomed >/dev/null
    echo doomed >>file
    git commit -am doomed >/dev/null
    git rev-parse HEAD
    git checkout main >/dev/null
    git branch -D doomed >/dev/null
  )"
  run_case "deleted-branch-commit" 1 "is not on main; refusing to release" "$doomed"

  local nomain
  nomain="$tmp/nomain"
  git clone "$origin" "$nomain" >/dev/null
  git -C "$nomain" checkout -B main origin/main >/dev/null
  git -C "$nomain" update-ref -d refs/remotes/origin/main
  status=0
  out="$(cd "$nomain" && "$script" HEAD 2>&1)" || status=$?
  echo "self-test: missing-origin-main → exit ${status} (expected 1)"
  echo "$out"
  if [[ "$status" -ne 1 ]]; then
    echo "self-test: missing-origin-main: expected exit 1, got ${status}" >&2
    failed=1
  fi
  if ! grep -F -q -- "origin/main not available; checkout needs fetch-depth: 0" <<<"$out"; then
    echo "self-test: missing-origin-main: missing fail-closed message" >&2
    failed=1
  fi

  run_case "nonexistent-ref" 1 "cannot resolve" definitely-not-a-ref

  if [[ "$failed" -ne 0 ]]; then
    echo "check-ref-on-main: self-test failed" >&2
    return 1
  fi
  echo "check-ref-on-main: self-test passed"
}

case "${1-}" in
  --self-test)
    self_test
    ;;
  -h|--help)
    usage
    ;;
  *)
    check_ref "${1:-HEAD}"
    ;;
esac
