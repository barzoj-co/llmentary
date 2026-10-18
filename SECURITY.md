# Security policy

## Reporting a vulnerability

Do not open a public issue for a security vulnerability.

Report privately through GitHub security advisories:

https://github.com/barzoj-co/llm-test-lab/security/advisories/new

That form is available once the repository is public. Until then, contact the
maintainers through a private GitHub channel (do not paste secrets or provider
API keys).

Please include:

- the proxy version (`llmtestlab --version` or the release tag);
- OS and architecture;
- a minimal reproduction without API keys, `Authorization` / `api-key` headers,
  or private prompts;
- impact (credential leak, request smuggling, control-API exposure, etc.).

## Supported versions

While the project is pre-1.0, only the latest `0.x` minor release is supported.

## Scope

In scope: this repository's recording proxy. That includes credential handling
and redaction, control API exposure, request smuggling or forwarding, and trace
storage.

Out of scope:

- bugs in upstream LLM providers;
- user misconfiguration, such as binding the control API to a public address
  without a token;
- third-party client libraries in other repositories.

## Response expectations

This is a solo-maintained project. Reports are handled on a best-effort basis.
There is no promised SLA.

## Release artifacts

GitHub Release archives are signed with cosign keyless (GitHub Actions OIDC)
and carry SLSA provenance. After downloading `checksums.txt` and the archives:

```bash
set -euo pipefail
cosign verify-blob \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity-regexp '^https://github.com/barzoj-co/llm-test-lab/\.github/workflows/goreleaser\.yml@refs/(heads/main|tags/v[0-9]+\.[0-9]+\.[0-9]+.*)$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-github-workflow-repository barzoj-co/llm-test-lab \
  checksums.txt
sha256sum -c --ignore-missing checksums.txt
gh attestation verify llm-test-lab_<version>_linux_amd64.tar.gz --repo barzoj-co/llm-test-lab
```
