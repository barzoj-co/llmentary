# Contributing

Maintainer and contributor documentation. You don't need any of this to *use*
llmentary.

- [CONTRIBUTING.md](https://github.com/barzoj-co/llmentary/blob/main/CONTRIBUTING.md):
  toolchain, OpenAPI codegen, tests, commit conventions and CI hygiene for the proxy.
- [Releasing](releasing.md): how proxy releases are cut, signed and verified.
- [Code of conduct](https://github.com/barzoj-co/llmentary/blob/main/CODE_OF_CONDUCT.md)
  and [security policy](https://github.com/barzoj-co/llmentary/blob/main/SECURITY.md).

Each client repository has its own contributing and release notes:
[llmentary-go](https://github.com/barzoj-co/llmentary-go/blob/main/CONTRIBUTING.md),
[llmentary-python](https://github.com/barzoj-co/llmentary-python/blob/main/CONTRIBUTING.md),
[llmentary-js](https://github.com/barzoj-co/llmentary-js/blob/main/CONTRIBUTING.md).

## Docs site

This site is built with [MkDocs Material](https://squidfunk.github.io/mkdocs-material/)
from `docs/` and `mkdocs.yml`. Preview locally:

```bash
python3 -m venv .venv
.venv/bin/pip install --require-hashes -r website/requirements.txt
.venv/bin/mkdocs serve
```

Pages under `docs/` are also read on GitHub, so keep links relative. Links that
leave `docs/` (for example `../SECURITY.md`) are rewritten to GitHub URLs by
`website/hooks.py`. CI runs `mkdocs build --strict` on pull requests and deploys
to GitHub Pages on push to `main`.
