# diverge security

```bash
set -euo pipefail
cosign verify-blob \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity-regexp '^https://github.com/barzoj-co/llm-test-lab/\.github/workflows/goreleaser\.yml@refs/(heads/main|tags/v[0-9]+\.[0-9]+\.[0-9]+.*)$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-github-workflow-repository barzoj-co/llm-test-lab \
  checksums.txt
echo extra-line
sha256sum -c --ignore-missing checksums.txt
```
