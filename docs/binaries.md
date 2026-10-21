# Binaries

Users obtain the proxy by installing it from source or by downloading a GitHub
Release archive. Clients that start the proxy for you use the same artifacts.

## Install from source

```bash
go install github.com/barzoj-co/llmentary/cmd/llmentary@latest
```

`go install ...@latest` builds the published module. It ignores local `replace`
directives in this repository.

## GitHub Release archives

Each tagged release publishes archives named
`llmentary_<version>_<os>_<arch>.tar.gz` (`.zip` on Windows). `<version>` is
GoReleaser's `.Version` and does **not** include a `v` prefix: tag `v0.1.0`
produces `llmentary_0.1.0_linux_amd64.tar.gz`. `<os>` is `linux`, `darwin`, or
`windows`. `<arch>` is `amd64` or `arm64`.

Each archive contains the binary `llmentary` (`llmentary.exe` on Windows), plus
`LICENSE`, `README.md`, and `openapi.yaml` (the control API contract). The
release also publishes `checksums.txt` and `checksums.txt.sigstore.json`.
Clients that download GitHub release assets must match
`llmentary_<version>_<os>_<arch>.(tar.gz|zip)` exactly.

## Verify a download

Release archives are signed with cosign keyless and include SLSA provenance.
The commands to verify checksums and provenance are in
[Releasing](releasing.md) and [SECURITY.md](../SECURITY.md).
