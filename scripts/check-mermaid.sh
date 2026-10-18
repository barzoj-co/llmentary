#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Extract mermaid fences from tracked Markdown and render each with mmdc.
# Usage:
#   scripts/check-mermaid.sh              # scan repo (excludes testdata/mermaid/)
#   scripts/check-mermaid.sh --self-test  # scan, then fail-closed self-tests
set -euo pipefail

MERMAID_CLI_VERSION="${MERMAID_CLI_VERSION:-11.12.0}"
MMDC="${MMDC:-mmdc}"
SELF_TEST=0
PARSE_ERR_RE="parse error|syntax error|lexical error|got '?PS'?"

usage() {
  cat <<'EOF'
Usage: scripts/check-mermaid.sh [--self-test]

Extract every CommonMark mermaid fence from tracked *.md files (excluding
testdata/mermaid/) and render each with a pinned mermaid-cli (mmdc).
Fails with the Markdown file and opening-fence line of any block that
does not parse. Fails closed if a required tool is missing or extraction
finds nothing while README.md still contains a mermaid fence.

  --self-test  also prove fail-closed tool checks, alternate fences, and
               testdata/mermaid/bad.md
EOF
}

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

require_cmd() {
  local name="$1"
  if ! command -v "$name" >/dev/null 2>&1; then
    echo "required tool not found: ${name}" >&2
    exit 1
  fi
}

require_cmd python3
require_cmd node
require_cmd npx
if ! command -v "$MMDC" >/dev/null 2>&1; then
  echo "required tool not found: ${MMDC} (install @mermaid-js/mermaid-cli@${MERMAID_CLI_VERSION})" >&2
  exit 1
fi

mmdc_ver="$("$MMDC" --version 2>/dev/null | head -n1 | tr -d '[:space:]')"
if [[ -n "$mmdc_ver" && "$mmdc_ver" != "$MERMAID_CLI_VERSION" ]]; then
  echo "mmdc version ${mmdc_ver} != pinned ${MERMAID_CLI_VERSION}" >&2
  exit 1
fi

root="$(git rev-parse --show-toplevel)"
cd "$root"

workdir="$(mktemp -d "${TMPDIR:-/tmp}/check-mermaid.XXXXXX")"
cleanup() { rm -rf "$workdir"; }
trap cleanup EXIT

puppeteer_config="${workdir}/puppeteer-config.json"
cat >"$puppeteer_config" <<'EOF'
{
  "args": ["--no-sandbox", "--disable-setuid-sandbox", "--disable-dev-shm-usage"],
  "timeout": 30000
}
EOF

# Render one .mmd file. Writes mmdc output to log_path. Returns mmdc status
# (or 1 if the SVG is missing / contains a parse error).
render_mmd() {
  local mmd_path="$1"
  local svg_path="$2"
  local log_path="$3"
  local status=0
  if command -v timeout >/dev/null 2>&1; then
    timeout 60 "$MMDC" -i "$mmd_path" -o "$svg_path" -p "$puppeteer_config" -q \
      >"$log_path" 2>&1 || status=$?
  else
    "$MMDC" -i "$mmd_path" -o "$svg_path" -p "$puppeteer_config" -q \
      >"$log_path" 2>&1 || status=$?
  fi
  if [[ "$status" -ne 0 ]]; then
    return "$status"
  fi
  if [[ ! -s "$svg_path" ]]; then
    echo "mmdc produced no SVG" >>"$log_path"
    return 1
  fi
  if grep -qiE "$PARSE_ERR_RE" "$svg_path"; then
    echo "mmdc SVG contains a parse/syntax error" >>"$log_path"
    return 1
  fi
  return 0
}

# Write each mermaid block in md_path to dest/block-<n>.mmd and print
# "path<TAB>start_line<TAB>mmd_path<TAB>closed(0|1)" per block.
# CommonMark fences: backtick or tilde, 3+ characters, indent 0-3 spaces,
# closing fence at least as long as the opening fence.
extract_blocks() {
  local md_path="$1"
  local dest="$2"
  python3 - "$md_path" "$dest" <<'PY'
import pathlib
import re
import sys

md_path = pathlib.Path(sys.argv[1])
dest = pathlib.Path(sys.argv[2])
dest.mkdir(parents=True, exist_ok=True)
text = md_path.read_text(encoding="utf-8")
lines = text.splitlines(keepends=True)
open_re = re.compile(r"^( {0,3})(`{3,}|~{3,})[ \t]*([^\r\n]*?)[ \t]*\r?$")
n = 0
i = 0
while i < len(lines):
    raw = lines[i].rstrip("\r\n")
    m = open_re.match(raw)
    if not m:
        i += 1
        continue
    indent, fence, info = m.group(1), m.group(2), m.group(3)
    fence_char = fence[0]
    if fence_char == "`" and "`" in info:
        i += 1
        continue
    info_word = re.split(r"[ \t]", info, maxsplit=1)[0].lower() if info else ""
    if info_word != "mermaid":
        i += 1
        continue
    start = i + 1
    i += 1
    body = []
    closed = False
    close_re = re.compile(
        r"^ {0,3}" + re.escape(fence_char) + r"{" + str(len(fence)) + r",}[ \t]*\r?$"
    )
    strip = len(indent)
    while i < len(lines):
        cur = lines[i]
        if close_re.match(cur.rstrip("\r\n")):
            closed = True
            break
        if strip and cur.startswith(" " * strip):
            body.append(cur[strip:])
        else:
            body.append(cur)
        i += 1
    mmd = dest / f"block-{n}.mmd"
    mmd.write_text("".join(body), encoding="utf-8")
    print(f"{md_path}\t{start}\t{mmd}\t{1 if closed else 0}")
    n += 1
    i += 1
PY
}

readme_has_mermaid_fence() {
  python3 - "$root/README.md" <<'PY'
import pathlib
import re
import sys

text = pathlib.Path(sys.argv[1]).read_text(encoding="utf-8")
open_re = re.compile(r"^( {0,3})(`{3,}|~{3,})[ \t]*([^\r\n]*?)[ \t]*\r?$", re.MULTILINE)
for m in open_re.finditer(text):
    fence, info = m.group(2), m.group(3)
    if fence[0] == "`" and "`" in info:
        continue
    info_word = re.split(r"[ \t]", info, maxsplit=1)[0].lower() if info else ""
    if info_word == "mermaid":
        sys.exit(0)
sys.exit(1)
PY
}

check_file() {
  local md_path="$1"
  local dest="${workdir}/$(echo "$md_path" | tr '/.' '__')"
  local listing="${dest}/listing"
  mkdir -p "$dest"
  if ! extract_blocks "$md_path" "$dest" >"$listing"; then
    echo "FAIL ${md_path} mermaid extraction failed" >&2
    return 1
  fi
  if [[ ! -s "$listing" ]]; then
    echo "OK  ${md_path} (no mermaid blocks)"
    return 0
  fi
  local failed=0
  local file start mmd closed
  while IFS=$'\t' read -r file start mmd closed; do
    local svg="${mmd%.mmd}.svg"
    local log="${mmd%.mmd}.log"
    if [[ "$closed" != "1" ]]; then
      echo "FAIL ${file}:${start} unclosed mermaid fence" >&2
      failed=1
      continue
    fi
    if [[ ! -s "$mmd" ]]; then
      echo "FAIL ${file}:${start} empty mermaid block" >&2
      failed=1
      continue
    fi
    rm -f "$svg"
    if render_mmd "$mmd" "$svg" "$log"; then
      echo "OK  ${file}:${start} mermaid parsed OK"
    else
      echo "FAIL ${file}:${start} mermaid did not parse" >&2
      cat "$log" >&2
      failed=1
    fi
  done <"$listing"
  return "$failed"
}

scan_failed=0
extracted_blocks=0
while IFS= read -r md; do
  case "$md" in
    testdata/mermaid/*) continue ;;
  esac
  dest="${workdir}/$(echo "$md" | tr '/.' '__')"
  listing="${dest}/listing"
  if ! check_file "$md"; then
    scan_failed=1
  fi
  if [[ -f "$listing" ]]; then
    extracted_blocks=$((extracted_blocks + $(wc -l <"$listing")))
  fi
done < <(git ls-files '*.md')

if [[ "$scan_failed" -ne 0 ]]; then
  echo "docs-mermaid: one or more mermaid blocks failed to parse" >&2
  exit 1
fi

if [[ "$extracted_blocks" -eq 0 ]] && readme_has_mermaid_fence; then
  echo "docs-mermaid: extracted 0 mermaid blocks but README.md contains a mermaid fence" >&2
  exit 1
fi

if [[ "$SELF_TEST" -eq 0 ]]; then
  exit 0
fi

echo "Self-test: missing python3 must fail closed"
bash_bin="$(command -v bash)"
empty_bin="${workdir}/empty-bin"
mkdir -p "$empty_bin"
set +e
PATH="$empty_bin" "$bash_bin" "$root/scripts/check-mermaid.sh" \
  >"${workdir}/nopython.out" 2>"${workdir}/nopython.err"
nopython_status=$?
set -e
if [[ "$nopython_status" -eq 0 ]]; then
  echo "FAIL self-test: script passed with python3 missing" >&2
  cat "${workdir}/nopython.out" >&2
  cat "${workdir}/nopython.err" >&2
  exit 1
fi
if ! grep -qi "python3" "${workdir}/nopython.err"; then
  echo "FAIL self-test: missing python3 did not mention python3:" >&2
  cat "${workdir}/nopython.err" >&2
  exit 1
fi
echo "OK  missing python3 failed closed"

echo "Self-test: testdata/mermaid/fences.md tilde and 4-backtick fences"
fences="testdata/mermaid/fences.md"
if [[ ! -f "$fences" ]]; then
  echo "FAIL self-test: ${fences} is missing" >&2
  exit 1
fi
fences_dir="${workdir}/fences"
mkdir -p "$fences_dir"
fences_listing="${fences_dir}/listing"
if ! extract_blocks "$fences" "$fences_dir" >"$fences_listing"; then
  echo "FAIL self-test: extraction of ${fences} failed" >&2
  exit 1
fi
fence_count=0
if [[ -s "$fences_listing" ]]; then
  fence_count="$(wc -l <"$fences_listing")"
fi
if [[ "$fence_count" -lt 2 ]]; then
  echo "FAIL self-test: ${fences} must yield at least 2 mermaid blocks (tilde + 4-backtick), got ${fence_count}" >&2
  cat "$fences_listing" >&2
  exit 1
fi
while IFS=$'\t' read -r file start mmd closed; do
  svg="${mmd%.mmd}.svg"
  log="${mmd%.mmd}.log"
  if [[ "$closed" != "1" ]]; then
    echo "FAIL self-test: ${file}:${start} unclosed fence" >&2
    exit 1
  fi
  rm -f "$svg"
  if ! render_mmd "$mmd" "$svg" "$log"; then
    echo "FAIL self-test: ${file}:${start} fence fixture did not parse" >&2
    cat "$log" >&2
    exit 1
  fi
  echo "OK  ${file}:${start} alternate fence parsed OK"
done <"$fences_listing"

echo "Self-test: testdata/mermaid/bad.md must be rejected with a parse error"
bad="testdata/mermaid/bad.md"
if [[ ! -f "$bad" ]]; then
  echo "FAIL self-test: ${bad} is missing" >&2
  exit 1
fi

self_dir="${workdir}/self-test"
mkdir -p "$self_dir"
self_listing="${self_dir}/listing"
if ! extract_blocks "$bad" "$self_dir" >"$self_listing"; then
  echo "FAIL self-test: extraction of ${bad} failed" >&2
  exit 1
fi
if [[ ! -s "$self_listing" ]]; then
  echo "FAIL self-test: ${bad} has no mermaid block" >&2
  exit 1
fi

self_failed_as_expected=0
while IFS=$'\t' read -r file start mmd closed; do
  svg="${mmd%.mmd}.svg"
  log="${mmd%.mmd}.log"
  rm -f "$svg"
  if render_mmd "$mmd" "$svg" "$log"; then
    echo "FAIL self-test: ${file}:${start} parsed OK (expected the original main README parse error)" >&2
    exit 1
  fi
  if ! grep -qiE "$PARSE_ERR_RE" "$log" && ! { [[ -f "$svg" ]] && grep -qiE "$PARSE_ERR_RE" "$svg"; }; then
    echo "FAIL self-test: ${file}:${start} failed, but not with a mermaid parse error:" >&2
    cat "$log" >&2
    exit 1
  fi
  echo "OK  ${file}:${start} rejected with expected parse error:"
  cat "$log"
  self_failed_as_expected=1
done <"$self_listing"

if [[ "$self_failed_as_expected" -ne 1 ]]; then
  echo "FAIL self-test: no mermaid block was checked" >&2
  exit 1
fi
echo "docs-mermaid: scan passed; fail-closed self-tests passed"
