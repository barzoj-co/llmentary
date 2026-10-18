#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Extract ```mermaid blocks from tracked Markdown and render each with mmdc.
# Usage:
#   scripts/check-mermaid.sh              # scan repo (excludes testdata/mermaid/)
#   scripts/check-mermaid.sh --self-test  # scan, then reject testdata/mermaid/bad.md
set -euo pipefail

MERMAID_CLI_VERSION="${MERMAID_CLI_VERSION:-11.12.0}"
MMDC="${MMDC:-mmdc}"
SELF_TEST=0
PARSE_ERR_RE="parse error|syntax error|lexical error|got '?PS'?"

usage() {
  cat <<'EOF'
Usage: scripts/check-mermaid.sh [--self-test]

Extract every ```mermaid block from tracked *.md files (excluding
testdata/mermaid/) and render each with a pinned mermaid-cli (mmdc).
Fails with the Markdown file and opening-fence line of any block that
does not parse.

  --self-test  also render testdata/mermaid/bad.md and require a parse error
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

if ! command -v "$MMDC" >/dev/null 2>&1; then
  echo "mmdc not found on PATH (install @mermaid-js/mermaid-cli@${MERMAID_CLI_VERSION})" >&2
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

# Write each mermaid block in md_path to workdir/block-<n>.mmd and print
# "path<TAB>start_line<TAB>mmd_path<TAB>closed(0|1)" per block.
extract_blocks() {
  local md_path="$1"
  local dest="$2"
  python3 - "$md_path" "$dest" <<'PY'
import pathlib, re, sys
md_path = pathlib.Path(sys.argv[1])
dest = pathlib.Path(sys.argv[2])
dest.mkdir(parents=True, exist_ok=True)
text = md_path.read_text(encoding="utf-8")
lines = text.splitlines(keepends=True)
open_re = re.compile(r"^```[ \t]*mermaid[ \t]*\r?$", re.IGNORECASE)
close_re = re.compile(r"^```[ \t]*\r?$")
n = 0
i = 0
while i < len(lines):
    if open_re.match(lines[i]):
        start = i + 1
        i += 1
        body = []
        closed = False
        while i < len(lines):
            if close_re.match(lines[i]):
                closed = True
                break
            body.append(lines[i])
            i += 1
        mmd = dest / f"block-{n}.mmd"
        mmd.write_text("".join(body), encoding="utf-8")
        print(f"{md_path}\t{start}\t{mmd}\t{1 if closed else 0}")
        n += 1
    i += 1
PY
}

check_file() {
  local md_path="$1"
  local dest="${workdir}/$(echo "$md_path" | tr '/.' '__')"
  local listing="${dest}/listing"
  mkdir -p "$dest"
  extract_blocks "$md_path" "$dest" >"$listing"
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
while IFS= read -r md; do
  case "$md" in
    testdata/mermaid/*) continue ;;
  esac
  if ! check_file "$md"; then
    scan_failed=1
  fi
done < <(git ls-files '*.md')

if [[ "$scan_failed" -ne 0 ]]; then
  echo "docs-mermaid: one or more mermaid blocks failed to parse" >&2
  exit 1
fi

if [[ "$SELF_TEST" -eq 0 ]]; then
  exit 0
fi

echo "Self-test: testdata/mermaid/bad.md must be rejected with a parse error"
bad="testdata/mermaid/bad.md"
if [[ ! -f "$bad" ]]; then
  echo "FAIL self-test: ${bad} is missing" >&2
  exit 1
fi

self_dir="${workdir}/self-test"
mkdir -p "$self_dir"
self_listing="${self_dir}/listing"
extract_blocks "$bad" "$self_dir" >"$self_listing"
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
echo "docs-mermaid: scan passed; negative fixture rejected"
