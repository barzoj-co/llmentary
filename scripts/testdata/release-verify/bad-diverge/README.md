# diverge readme

```bash
set -euo pipefail
cosign verify-blob \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity-regexp '^https://github.com/barzoj-co/llmentary/\.github/workflows/goreleaser\.yml@refs/(heads/main|tags/v[0-9]+\.[0-9]+\.[0-9]+.*)$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-github-workflow-repository barzoj-co/llmentary \
  checksums.txt
sha256sum -c --ignore-missing checksums.txt
```
