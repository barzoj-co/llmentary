#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Fail if a workflow places a secret where PR-controlled or install steps can read it.
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: scripts/check-secret-scope.sh [--self-test] [--e2e-job <id>] [--e2e-environment <name>] [--openai-secret <name>]

Scan .github/workflows/*.y*ml for secret-placement violations.
A file named ci.yml must contain job e2e-gate with if: containing always()
and needs containing the e2e job id (default e2e), and that e2e job must set
environment: openai-e2e (or --e2e-environment). No other job may receive
OPENAI_API_KEY (or --openai-secret) or that environment.

  --e2e-job <id>           e2e job id that e2e-gate must need (default: e2e)
  --e2e-environment <name> environment the e2e job must use (default: openai-e2e)
  --openai-secret <name>   live-e2e secret that only the e2e job may use (default: OPENAI_API_KEY)
  --self-test              accept scripts/testdata/secret-scope/good and reject each bad fixture
EOF
}

SELF_TEST=0
E2E_JOB=e2e
E2E_ENVIRONMENT=openai-e2e
OPENAI_SECRET=OPENAI_API_KEY
while [[ $# -gt 0 ]]; do
  case "$1" in
    --self-test) SELF_TEST=1; shift ;;
    --e2e-job)
      if [[ $# -lt 2 ]]; then
        echo "--e2e-job requires an argument" >&2
        exit 2
      fi
      E2E_JOB="$2"
      shift 2
      ;;
    --e2e-environment)
      if [[ $# -lt 2 ]]; then
        echo "--e2e-environment requires an argument" >&2
        exit 2
      fi
      E2E_ENVIRONMENT="$2"
      shift 2
      ;;
    --openai-secret)
      if [[ $# -lt 2 ]]; then
        echo "--openai-secret requires an argument" >&2
        exit 2
      fi
      OPENAI_SECRET="$2"
      shift 2
      ;;
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
  E2E_JOB="$E2E_JOB" E2E_ENVIRONMENT="$E2E_ENVIRONMENT" OPENAI_SECRET="$OPENAI_SECRET" python3 - "$@" <<'PY'
from __future__ import annotations

import os
import pathlib
import re
import sys

import yaml

E2E_JOB = os.environ.get("E2E_JOB", "e2e")
E2E_ENVIRONMENT = os.environ.get("E2E_ENVIRONMENT", "openai-e2e")
OPENAI_SECRET = os.environ.get("OPENAI_SECRET", "OPENAI_API_KEY")
INSTALL_CMDS = (
    "npm ci",
    "npm install",
    "npm i ",
    "pnpm install",
    "yarn install",
    "pip install",
    "python -m pip install",
    "uv sync",
    "uv pip install",
    "poetry install",
    "go mod download",
    "go get",
)
EXPR_RE = re.compile(r"\$\{\{\s*(.*?)\s*\}\}", re.S)
BOOL_RE = re.compile(
    r"""secrets\.([A-Za-z0-9_]+)\s*(?:!=|==)\s*(?:''|"")"""
)
RAW_NAME_RE = re.compile(r"secrets\.([A-Za-z0-9_]+)")
INDEX_NAME_RE = re.compile(r"""secrets\[\s*['"]([A-Za-z0-9_]+)['"]\s*\]""")
WHOLE_SECRETS_RE = re.compile(r"\bsecrets\b(?!\s*\.)")
GITHUB_TOKEN_NAMES = {"GITHUB_TOKEN"}


def load(path: pathlib.Path):
    with path.open(encoding="utf-8") as fh:
        return yaml.safe_load(fh)


def trigger_on(doc: dict):
    on = doc.get("on")
    if on is None:
        on = doc.get(True)
    return on


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


def line_of_secret(path: pathlib.Path) -> int:
    for needle in (
        f"secrets.{OPENAI_SECRET}",
        f"secrets['{OPENAI_SECRET}']",
        f'secrets["{OPENAI_SECRET}"]',
        "toJSON(secrets)",
        "secrets[",
        "secrets.",
    ):
        line = line_of(path, needle, default=0)
        if line:
            return line
    return 1


def line_of(path: pathlib.Path, needle: str, default: int = 1) -> int:
    try:
        for i, line in enumerate(path.read_text(encoding="utf-8").splitlines(), start=1):
            stripped = line.lstrip()
            if stripped.startswith("#"):
                continue
            if needle in line:
                return i
    except OSError:
        return default
    return default


def secret_refs(text: str) -> list[tuple[str, bool]]:
    if not isinstance(text, str):
        return []
    refs: list[tuple[str, bool]] = []
    for m in EXPR_RE.finditer(text):
        expr = " ".join(m.group(1).split())
        if expr == "github.token":
            refs.append(("GITHUB_TOKEN", True))
            continue
        bm = BOOL_RE.fullmatch(expr)
        if bm:
            refs.append((bm.group(1), False))
            continue
        rm = re.fullmatch(r"secrets\.([A-Za-z0-9_]+)", expr)
        if rm:
            refs.append((rm.group(1), True))
            continue
        if "github.token" in expr:
            refs.append(("GITHUB_TOKEN", True))
        for sm in RAW_NAME_RE.finditer(expr):
            refs.append((sm.group(1), True))
        for sm in INDEX_NAME_RE.finditer(expr):
            refs.append((sm.group(1), True))
        stripped = INDEX_NAME_RE.sub("", expr)
        stripped = RAW_NAME_RE.sub("", stripped)
        if WHOLE_SECRETS_RE.search(stripped):
            # toJSON(secrets) or secrets[dynamic] exposes every secret.
            refs.append((OPENAI_SECRET, True))
            refs.append(("*", True))
    return refs


def raw_secret_names(text: str) -> list[str]:
    return [name for name, raw in secret_refs(text) if raw]


def env_raw_names(env) -> list[str]:
    if not isinstance(env, dict):
        return []
    names: list[str] = []
    for value in env.values():
        if isinstance(value, str):
            names.extend(raw_secret_names(value))
    return names


def env_has_raw(env) -> bool:
    return bool(env_raw_names(env))


def steps_of(job: dict) -> list[dict]:
    steps = job.get("steps")
    if not isinstance(steps, list):
        return []
    return [s for s in steps if isinstance(s, dict)]


def environment_name(job: dict) -> str | None:
    env = job.get("environment")
    if isinstance(env, str):
        return env
    if isinstance(env, dict) and env.get("name") is not None:
        return str(env.get("name"))
    return None


def text_mentions_openai(text: str) -> bool:
    return any(name == OPENAI_SECRET for name, _ in secret_refs(text))


def job_receives_openai(job: dict) -> bool:
    env = job.get("env")
    if isinstance(env, dict):
        for value in env.values():
            if isinstance(value, str) and text_mentions_openai(value):
                return True
    secrets = job.get("secrets")
    if isinstance(secrets, dict):
        for key, value in secrets.items():
            if str(key) == OPENAI_SECRET:
                return True
            if isinstance(value, str) and text_mentions_openai(value):
                return True
    for step in steps_of(job):
        run = step.get("run")
        if isinstance(run, str) and text_mentions_openai(run):
            return True
        step_env = step.get("env")
        if isinstance(step_env, dict):
            for value in step_env.values():
                if isinstance(value, str) and text_mentions_openai(value):
                    return True
        with_ = step.get("with")
        if isinstance(with_, dict):
            for value in with_.values():
                if isinstance(value, str) and text_mentions_openai(value):
                    return True
    return False


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

    events = trigger_events(trigger_on(doc))
    if "pull_request_target" in events:
        line = line_of(path, "pull_request_target")
        print(f"{path}:{line}: pull_request_target is forbidden")
        offending += 1

    env = doc.get("env")
    if isinstance(env, dict):
        for value in env.values():
            if isinstance(value, str) and secret_refs(value):
                needle = next((n for n in ("secrets.", "github.token") if n in value), "env:")
                print(f"{path}:{line_of(path, needle)}: secret in workflow-level env")
                offending += 1
                break

    jobs = doc.get("jobs")
    if not isinstance(jobs, dict):
        jobs = {}

    for job_id, job in jobs.items():
        if not isinstance(job, dict):
            continue
        secrets = job.get("secrets")
        if secrets == "inherit":
            print(
                f"{path}:{line_of(path, 'inherit')}: secrets: inherit is forbidden; pass named secrets"
            )
            offending += 1

        job_env = job.get("env")
        if isinstance(job_env, dict):
            for value in job_env.values():
                if isinstance(value, str) and secret_refs(value):
                    needle = next((n for n in ("secrets.", "github.token") if n in value), "env:")
                    print(f"{path}:{line_of(path, needle)}: secret in job-level env")
                    offending += 1
                    break

        raw_steps: dict[str, int] = {}
        for step in steps_of(job):
            run = step.get("run")
            if isinstance(run, str) and secret_refs(run):
                needle = next((n for n in ("secrets.", "github.token") if n in run), "run:")
                print(
                    f"{path}:{line_of(path, needle)}: secret interpolated into run:; pass it via step env"
                )
                offending += 1

            uses = str(step.get("uses") or "")
            with_ = step.get("with")
            if isinstance(with_, dict):
                for key, value in with_.items():
                    if not isinstance(value, str) or not secret_refs(value):
                        continue
                    if key == "token" and uses.startswith("actions/checkout@"):
                        continue
                    needle = next((n for n in ("secrets.", "github.token") if n in value), str(key))
                    print(f"{path}:{line_of(path, needle)}: secret in with:")
                    offending += 1

            step_env = step.get("env")
            if isinstance(run, str) and env_has_raw(step_env) and any(cmd in run for cmd in INSTALL_CMDS):
                print(f"{path}:{line_of(path, 'secrets.')}: secret on an install step")
                offending += 1

            seen_in_step: set[str] = set()
            if isinstance(step_env, dict):
                for value in step_env.values():
                    if not isinstance(value, str):
                        continue
                    for name in raw_secret_names(value):
                        if name in GITHUB_TOKEN_NAMES:
                            continue
                        seen_in_step.add(name)
            for name in seen_in_step:
                raw_steps[name] = raw_steps.get(name, 0) + 1

        for name, count in raw_steps.items():
            if count > 1:
                print(
                    f"{path}:{line_of(path, 'secrets.' + name)}: "
                    f"secret {name} used raw in more than one step of job {job_id}; "
                    f"presence checks must use the boolean form"
                )
                offending += 1

        receives = job_receives_openai(job)
        if receives and job_id != E2E_JOB:
            print(
                f"{path}:{line_of_secret(path)}: "
                f"secret {OPENAI_SECRET} used outside job {E2E_JOB}"
            )
            offending += 1
        elif receives and environment_name(job) != E2E_ENVIRONMENT:
            print(
                f"{path}:{line_of_secret(path)}: "
                f"secret {OPENAI_SECRET} requires environment {E2E_ENVIRONMENT}"
            )
            offending += 1
        if job_id != E2E_JOB and environment_name(job) == E2E_ENVIRONMENT:
            print(
                f"{path}:{line_of(path, E2E_ENVIRONMENT)}: "
                f"environment {E2E_ENVIRONMENT} used outside job {E2E_JOB}"
            )
            offending += 1

    if path.name == "ci.yml":
        gate = jobs.get("e2e-gate")
        ok = isinstance(gate, dict)
        if ok:
            if_val = str(gate.get("if") or "")
            needs = gate.get("needs")
            if isinstance(needs, list):
                need_ids = [str(x) for x in needs]
            elif needs is None:
                need_ids = []
            else:
                need_ids = [str(needs)]
            ok = "always()" in if_val and E2E_JOB in need_ids
        if not ok:
            print(f"{path}:{line_of(path, 'e2e-gate')}: missing or malformed e2e-gate job")
            offending += 1
        e2e_job = jobs.get(E2E_JOB)
        env_ok = isinstance(e2e_job, dict) and environment_name(e2e_job) == E2E_ENVIRONMENT
        if not env_ok:
            print(
                f"{path}:{line_of(path, 'environment:')}: "
                f"missing or malformed environment on {E2E_JOB} job"
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
  echo "check-secret-scope: ${#files[@]} workflow(s) ok"
else
  echo "check-secret-scope: secret-scope violations" >&2
  exit 1
fi

if [[ "$SELF_TEST" -eq 0 ]]; then
  exit 0
fi

good_dir="scripts/testdata/secret-scope/good"
bad_dir="scripts/testdata/secret-scope/bad"
if [[ ! -d "$good_dir" || ! -d "$bad_dir" ]]; then
  echo "self-test fixtures missing under scripts/testdata/secret-scope/" >&2
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
  expect=""
  first="$(head -n 1 "$f" || true)"
  if [[ "$first" == "# expect:"* ]]; then
    expect="${first#\# expect:}"
    expect="${expect#"${expect%%[![:space:]]*}"}"
  fi
  status=0
  out="$(scan_files "$f" 2>&1)" || status=$?
  if [[ "$status" -eq 0 ]]; then
    echo "self-test: ${f} exited 0 (expected nonzero)" >&2
    self_failed=1
    continue
  fi
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
  if [[ -z "$expect" ]]; then
    echo "self-test: ${f} missing '# expect: <substring>' on first line" >&2
    self_failed=1
    continue
  fi
  if ! grep -F -q -- "$expect" <<<"$out"; then
    echo "self-test: ${f} rejected without expected reason ${expect@Q}:" >&2
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
echo "check-secret-scope: self-test passed"
