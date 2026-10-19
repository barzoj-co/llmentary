# missing required flags

```bash
cosign verify-blob \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity-regexp '^https://github.com/barzoj-co/llmentary/\.github/workflows/goreleaser\.yml@refs/tags/v.*$' \
  checksums.txt
sha256sum -c --ignore-missing checksums.txt
```
