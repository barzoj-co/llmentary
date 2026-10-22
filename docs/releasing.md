# Releasing

Merging the release-please PR tags the next version and publishes GitHub
Release notes. GoReleaser then attaches signed archives, checksums, and SBOMs
to that existing release. Later versions come from conventional commits
(`feat`, `fix`). While the package is 0.x, both bump patch (`0.5.0` → `0.5.1`),
not minor. Do not use `!` or a `BREAKING CHANGE:` footer; a 1.0 bump
needs an explicit maintainer decision.

`api/control/openapi.yaml` `info.version` is bumped by a generic extra-file
marker (`# x-release-please-version` on that line). Do not use the YAML
jsonpath updater for this file: it re-serializes the whole document and
folds long strings, which is not a source-of-truth rewrite.

In an emergency, push an annotated tag `vX.Y.Z` to `main`'s commit. The
`Release` workflow calls the same reusable GoReleaser job. Official releases
are built from a clean `vX.Y.Z` tag only (no dirty working tree, no extra
pre-release suffix). `--version` and `GET /version` print that clean version
and the commit. Do not create a second changelog on the GitHub Release.

## Provenance

GitHub Release archives are signed with cosign keyless (GitHub Actions OIDC)
and carry SLSA provenance. After downloading `checksums.txt` and the archives:

```bash
set -euo pipefail
cosign verify-blob \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity-regexp '^https://github.com/barzoj-co/llmentary/\.github/workflows/goreleaser\.yml@refs/(heads/main|tags/v[0-9]+\.[0-9]+\.[0-9]+.*)$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-github-workflow-repository barzoj-co/llmentary \
  checksums.txt
sha256sum -c --ignore-missing checksums.txt
VERSION=0.1.0 # downloaded release version, without the v prefix
gh attestation verify "llmentary_${VERSION}_linux_amd64.tar.gz" --repo barzoj-co/llmentary
```

Archive names and contents are documented in [Binaries](binaries.md).
