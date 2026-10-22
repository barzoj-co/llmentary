#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Fail unless the live e2e job succeeded, except a skipped fork PR.
# Reads E2E_RESULT, EVENT_NAME, IS_FORK.
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: scripts/e2e-gate.sh [--self-test]

Exit 0 when E2E_RESULT=success, or when a fork PR skipped live e2e
(IS_FORK=true and E2E_RESULT=skipped). Otherwise exit 1 with an ::error:: line.
Reads E2E_RESULT, EVENT_NAME, IS_FORK from the environment.

  --self-test  run the built-in case table
EOF
}

gate() {
  local result="${E2E_RESULT-}"
  local event="${EVENT_NAME-}"
  local fork="${IS_FORK-}"

  if [[ "$result" == "success" ]]; then
    return 0
  fi

  if [[ "$fork" == "true" && "$result" == "skipped" ]]; then
    echo "skipped (fork, no secrets)"
    return 0
  fi

  if [[ "$result" == "skipped" ]]; then
    echo "::error::live e2e did not run (event=${event}, result=skipped)"
    return 1
  fi

  echo "::error::live e2e did not succeed (result=${result})"
  return 1
}

self_test() {
  local failed=0
  run_case() {
    local name="$1" result="$2" event="$3" fork="$4" want_exit="$5" want_msg="${6-}"
    local status=0 out
    out="$(E2E_RESULT="$result" EVENT_NAME="$event" IS_FORK="$fork" gate 2>&1)" || status=$?
    echo "self-test: ${name} (E2E_RESULT=${result@Q} EVENT_NAME=${event@Q} IS_FORK=${fork@Q}) → exit ${status} (expected ${want_exit})"
    if [[ -n "$out" ]]; then
      echo "$out"
    fi
    if [[ "$status" -ne "$want_exit" ]]; then
      echo "self-test: ${name}: expected exit ${want_exit}, got ${status}" >&2
      failed=1
    fi
    if [[ -n "$want_msg" ]]; then
      if ! grep -F -q -- "$want_msg" <<<"$out"; then
        echo "self-test: ${name}: output did not contain ${want_msg@Q}" >&2
        failed=1
      fi
    elif [[ "$want_exit" -ne 0 ]]; then
      echo "self-test: ${name}: missing expected message" >&2
      failed=1
    fi
  }

  run_case "success/pull_request/false" "success" "pull_request" "false" 0
  run_case "success/pull_request/true" "success" "pull_request" "true" 0
  run_case "skipped/pull_request/true" "skipped" "pull_request" "true" 0 \
    "skipped (fork, no secrets)"
  out="$(E2E_RESULT=skipped EVENT_NAME=pull_request IS_FORK=true gate 2>&1)" || status=$?
  if [[ "${status:-0}" -ne 0 ]] || grep -F -q -- "::error::" <<<"$out"; then
    echo "self-test: fork skip must not emit ::error:: (exit=${status:-0} out=${out@Q})" >&2
    failed=1
  fi
  run_case "skipped/pull_request/false" "skipped" "pull_request" "false" 1 \
    "live e2e did not run (event=pull_request, result=skipped)"
  run_case "skipped/push/false" "skipped" "push" "false" 1 \
    "live e2e did not run (event=push, result=skipped)"
  run_case "skipped/empty-fork" "skipped" "pull_request" "" 1 \
    "live e2e did not run (event=pull_request, result=skipped)"
  run_case "skipped/workflow_dispatch/false" "skipped" "workflow_dispatch" "false" 1 \
    "live e2e did not run (event=workflow_dispatch, result=skipped)"
  run_case "failure/pull_request/true" "failure" "pull_request" "true" 1 \
    "live e2e did not succeed (result=failure)"
  run_case "cancelled/pull_request/true" "cancelled" "pull_request" "true" 1 \
    "live e2e did not succeed (result=cancelled)"
  run_case "failure" "failure" "pull_request" "false" 1 \
    "live e2e did not succeed (result=failure)"
  run_case "cancelled" "cancelled" "pull_request" "false" 1 \
    "live e2e did not succeed (result=cancelled)"
  run_case "empty-result" "" "pull_request" "false" 1 \
    "live e2e did not succeed (result=)"
  run_case "unknown-result" "unknown" "pull_request" "false" 1 \
    "live e2e did not succeed (result=unknown)"

  if [[ "$failed" -ne 0 ]]; then
    echo "e2e-gate: self-test failed" >&2
    return 1
  fi
  echo "e2e-gate: self-test passed"
}

case "${1-}" in
  --self-test)
    self_test
    ;;
  -h|--help)
    usage
    ;;
  "")
    gate
    ;;
  *)
    echo "unknown argument: $1" >&2
    usage >&2
    exit 2
    ;;
esac
