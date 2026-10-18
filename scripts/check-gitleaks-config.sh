#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Prove the committed gitleaks allowlist is not too broad.
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: scripts/check-gitleaks-config.sh --self-test

Write throwaway files and run `gitleaks dir --redact` with .gitleaks.toml:
  (a) sk-... and sk-invalid-key-for-401-test must exit 0
  (b) a constructed OpenAI key and GitHub PAT must exit 1, including under
      a path that looks like an allowlisted tests/e2e/ file

Output is redacted. Keys are built at runtime from random characters.
EOF
}

if [[ "${1-}" == "-h" || "${1-}" == "--help" ]]; then
  usage
  exit 0
fi
if [[ "${1-}" != "--self-test" ]]; then
  echo "Usage: scripts/check-gitleaks-config.sh --self-test" >&2
  exit 2
fi

if ! command -v gitleaks >/dev/null 2>&1; then
  echo "gitleaks is required on PATH" >&2
  exit 1
fi

root="$(git rev-parse --show-toplevel)"
config="${root}/.gitleaks.toml"
if [[ ! -f "$config" ]]; then
  echo "missing ${config}" >&2
  exit 1
fi

rand_alnum() {
  python3 -c "import secrets,string; a=string.ascii_letters+string.digits; print(''.join(secrets.choice(a) for _ in range(int('${1}'))))"
}

run_dir() {
  local target="$1"
  gitleaks dir --config "$config" --redact --no-banner --exit-code 1 "$target"
}

tmp="$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/gitleaks-allowlist.XXXXXX")"
trap 'rm -rf "$tmp"' EXIT

# (a) documented placeholders and synthetic 401-test key must stay clean.
ok_dir="$tmp/ok"
mkdir -p "$ok_dir"
printf '%s\n' 'OPENAI_API_KEY=sk-...' 'sk-invalid-key-for-401-test' >"${ok_dir}/placeholders.txt"
status=0
out="$(run_dir "$ok_dir" 2>&1)" || status=$?
echo "self-test: placeholders → exit ${status} (expected 0)"
echo "$out"
if [[ "$status" -ne 0 ]]; then
  echo "self-test: placeholders were flagged; allowlist is too narrow" >&2
  exit 1
fi

# (b) realistic-looking values, never stored as a literal in the repo.
# OpenAI default rule wants sk- + 20 + T3BlbkFJ + 20. GitHub PAT is ghp_ + 36.
oa_mid="$(printf '%s%s%s%s' T3 Bl bk FJ)"
oa_key="$(printf '%s%s%s%s%s' sk - "$(rand_alnum 20)" "$oa_mid" "$(rand_alnum 20)")"
pat_key="$(printf '%s%s%s' ghp _ "$(rand_alnum 36)")"

bad_dir="$tmp/bad"
mkdir -p "$bad_dir"
# Write via Python so the assembled values are not echoed by this script.
python3 - "$bad_dir" "$oa_key" "$pat_key" <<'PY'
import pathlib
import sys
root = pathlib.Path(sys.argv[1])
(root / "leak.txt").write_text(sys.argv[2] + "\n" + sys.argv[3] + "\n", encoding="utf-8")
nested = root / "tests" / "e2e"
nested.mkdir(parents=True)
(nested / "leak.txt").write_text(sys.argv[2] + "\n" + sys.argv[3] + "\n", encoding="utf-8")
PY
# Drop locals so later `set -x` or error dumps cannot print them.
unset oa_key pat_key oa_mid

status=0
out="$(run_dir "$bad_dir" 2>&1)" || status=$?
echo "self-test: constructed keys (including tests/e2e/) → exit ${status} (expected 1)"
echo "$out"
if [[ "$status" -eq 0 ]]; then
  echo "self-test: constructed keys were not flagged; allowlist is too broad" >&2
  exit 1
fi
if ! grep -E -q 'leaks found|Finding:' <<<"$out"; then
  echo "self-test: gitleaks did not report findings for constructed keys" >&2
  exit 1
fi

echo "check-gitleaks-config: self-test passed"
