#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Enforce least-privilege, concurrency, and timeouts on GitHub workflows.
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
cd "$root"

if ! python3 -c "import yaml" >/dev/null 2>&1; then
  echo "PyYAML is required: python3 -m pip install pyyaml" >&2
  exit 1
fi

python3 - <<'PY'
from __future__ import annotations

import pathlib
import sys

import yaml

ALLOWED_TOP_PERMISSIONS = ({}, {"contents": "read"})
ALLOWED_JOB_PERMISSIONS = {
    "contents": {"read", "write"},
    "actions": {"read"},
    "security-events": {"write"},
    "id-token": {"write"},
    "pull-requests": {"write"},
    "issues": {"read", "write"},
    "attestations": {"write"},
}
RELEASE_WORKFLOWS = {"release.yml", "release-please.yml"}


def load(path: pathlib.Path):
    with path.open(encoding="utf-8") as fh:
        return yaml.safe_load(fh)


def has_pull_request(on) -> bool:
    if on is None:
        return False
    if isinstance(on, str):
        return on == "pull_request"
    if isinstance(on, list):
        return any(has_pull_request(item) for item in on)
    if isinstance(on, dict):
        return "pull_request" in on
    return False


def concurrency_ok(conc, *, release: bool, pr: bool) -> str | None:
    if not isinstance(conc, dict):
        return "concurrency must be a mapping"
    group = conc.get("group")
    cancel = conc.get("cancel-in-progress")
    if not group:
        return "concurrency.group is required"
    if release:
        # Fixed group, never cancel an in-flight release.
        if isinstance(group, str) and ("pull_request" in group or "github.ref" in group):
            return "release concurrency.group must be a fixed name"
        if cancel is not False:
            return "release concurrency.cancel-in-progress must be false"
        return None
    if pr:
        if cancel is None:
            return "PR workflows must set concurrency.cancel-in-progress"
    return None


def check_permissions(perms, *, job: str | None) -> list[str]:
    errors: list[str] = []
    where = f"job {job}" if job else "top-level"
    if perms is None:
        if job is None:
            errors.append("missing top-level permissions:")
        return errors
    if not isinstance(perms, dict):
        errors.append(f"{where} permissions: must be a mapping or {{}}")
        return errors
    if job is None and perms not in ALLOWED_TOP_PERMISSIONS:
        errors.append(f"top-level permissions: must be contents: read or {{}}; got {perms}")
        return errors
    for key, value in perms.items():
        allowed = ALLOWED_JOB_PERMISSIONS.get(key)
        if allowed is None:
            errors.append(f"{where} grants unknown permission {key}: {value}")
            continue
        if value not in allowed:
            errors.append(f"{where} grants {key}: {value} (allowed: {sorted(allowed)})")
    return errors


errors: list[str] = []
files = sorted(pathlib.Path(".github/workflows").glob("*.y*ml"))
if not files:
    print("no workflow files under .github/workflows/", file=sys.stderr)
    sys.exit(1)

for path in files:
    name = path.name
    try:
        doc = load(path)
    except yaml.YAMLError as exc:
        errors.append(f"{path}: YAML error: {exc}")
        continue
    if not isinstance(doc, dict):
        errors.append(f"{path}: workflow must be a mapping")
        continue

    errors.extend(f"{path}: {e}" for e in check_permissions(doc.get("permissions"), job=None))

    on = doc.get("on") or doc.get(True)
    pr = has_pull_request(on)
    release = name in RELEASE_WORKFLOWS
    if pr or release:
        if "concurrency" not in doc:
            errors.append(f"{path}: missing concurrency:")
        else:
            msg = concurrency_ok(doc["concurrency"], release=release, pr=pr)
            if msg:
                errors.append(f"{path}: {msg}")

    jobs = doc.get("jobs")
    if not isinstance(jobs, dict) or not jobs:
        errors.append(f"{path}: missing jobs:")
        continue
    for job_id, job in jobs.items():
        if not isinstance(job, dict):
            errors.append(f"{path}: job {job_id} must be a mapping")
            continue
        uses = job.get("uses")
        reusable = isinstance(uses, str) and uses.startswith("./") and uses.endswith(
            (".yml", ".yaml")
        )
        # GitHub forbids timeout-minutes on reusable-workflow caller jobs.
        if "timeout-minutes" not in job and not reusable:
            errors.append(f"{path}: job {job_id} missing timeout-minutes")
        errors.extend(
            f"{path}: {e}" for e in check_permissions(job.get("permissions"), job=job_id)
        )

if errors:
    print("workflow policy violations:", file=sys.stderr)
    for err in errors:
        print(err, file=sys.stderr)
    sys.exit(1)
print(f"check-workflow-policy: {len(files)} workflow(s) ok")
PY
