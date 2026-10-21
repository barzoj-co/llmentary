# Changelog

## [0.4.1](https://github.com/barzoj-co/llmentary/compare/v0.4.0...v0.4.1) (2026-10-21)


### Bug Fixes

* fail isolated_from when the other session is empty ([#42](https://github.com/barzoj-co/llmentary/issues/42)) ([a2d44de](https://github.com/barzoj-co/llmentary/commit/a2d44de3dfd5721dd77facf2e91b43fb482a794e))
* hide in-flight calls until the settled record is published ([#43](https://github.com/barzoj-co/llmentary/issues/43)) ([46b9dc6](https://github.com/barzoj-co/llmentary/commit/46b9dc61d354be6c8db255b6a9419923a37b4cb1))
* say list sessions with GET /sessions on 404 ([#41](https://github.com/barzoj-co/llmentary/issues/41)) ([4333d80](https://github.com/barzoj-co/llmentary/commit/4333d806aac8963836c36ebb918cb674b82cea39))
* stop release-please reformatting openapi.yaml ([#47](https://github.com/barzoj-co/llmentary/issues/47)) ([2264a32](https://github.com/barzoj-co/llmentary/commit/2264a3202f9f94f1649807f50c8e911fd3a15ad8))
* treat client write errors as after-headers aborts ([#48](https://github.com/barzoj-co/llmentary/issues/48)) ([3a70488](https://github.com/barzoj-co/llmentary/commit/3a7048860f8ee54005920bc6ff6b58b4007f5658))

## [0.4.0](https://github.com/barzoj-co/llmentary/compare/v0.3.0...v0.4.0) (2026-10-21)


### Features

* add typed client_aborted and abort_phase on Call ([#39](https://github.com/barzoj-co/llmentary/issues/39)) ([8a9a031](https://github.com/barzoj-co/llmentary/commit/8a9a0310812098d4d9553cc8f035033f3d7f99d2))
* document sessions spec counters and emit PascalCase aliases ([#38](https://github.com/barzoj-co/llmentary/issues/38)) ([ba358d6](https://github.com/barzoj-co/llmentary/commit/ba358d67688474027967ff53700527e97976870a))


### Bug Fixes

* record mid-stream cancel as after_headers and log seq/spec ([#36](https://github.com/barzoj-co/llmentary/issues/36)) ([041b6c4](https://github.com/barzoj-co/llmentary/commit/041b6c47c1541eab3e7d0dae7097269033be9c5f))
* stamp OpenAPI info.version from the binary release ([#37](https://github.com/barzoj-co/llmentary/issues/37)) ([4ade02d](https://github.com/barzoj-co/llmentary/commit/4ade02d5ed89361fce57ee72666875d6e893e9cb))

## [0.3.0](https://github.com/barzoj-co/llmentary/compare/v0.2.2...v0.3.0) (2026-10-21)


### Features

* add spec= field on proxy response logs ([#32](https://github.com/barzoj-co/llmentary/issues/32)) ([8a16aaf](https://github.com/barzoj-co/llmentary/commit/8a16aafb9d2e0c031f9ceaa4544a85a70a7999ec))
* serve GET /openapi.yaml on the control API ([#28](https://github.com/barzoj-co/llmentary/issues/28)) ([828a5c8](https://github.com/barzoj-co/llmentary/commit/828a5c82beb68d945c586bfb7cfd37462bb2759c))


### Bug Fixes

* record client cancel before headers as client_aborted ([#31](https://github.com/barzoj-co/llmentary/issues/31)) ([6c4548d](https://github.com/barzoj-co/llmentary/commit/6c4548d90ad185f1e44db786af9b7da9d271cf58))
* return 404 for wait on a missing session when min_calls=0 ([#30](https://github.com/barzoj-co/llmentary/issues/30)) ([6153daf](https://github.com/barzoj-co/llmentary/commit/6153dafd33d245eef9346b7f68c742591b2f08a0))

## [0.2.2](https://github.com/barzoj-co/llmentary/compare/v0.2.1...v0.2.2) (2026-10-21)


### Bug Fixes

* count wait quiet from last session activity ([#25](https://github.com/barzoj-co/llmentary/issues/25)) ([d8c007d](https://github.com/barzoj-co/llmentary/commit/d8c007dcc6ef2684e905965c213080027d509950))
* do not mark complete SSE streams as client_aborted ([#24](https://github.com/barzoj-co/llmentary/issues/24)) ([7bc039e](https://github.com/barzoj-co/llmentary/commit/7bc039e1b8b0dd4cb13941369baa3d47602d9c86))
* name unknown spec fields and collapse streamed findings ([#26](https://github.com/barzoj-co/llmentary/issues/26)) ([10cc210](https://github.com/barzoj-co/llmentary/commit/10cc210a89373dcd60488b8b32f8bdb7bb93ade0))

## [0.2.1](https://github.com/barzoj-co/llmentary/compare/v0.2.0...v0.2.1) (2026-10-21)


### Bug Fixes

* build with Go 1.26.10 ([#22](https://github.com/barzoj-co/llmentary/issues/22)) ([4b60a8c](https://github.com/barzoj-co/llmentary/commit/4b60a8ccb7e2b14c708e2a0a1f81336f487660f9))

## [0.2.0](https://github.com/barzoj-co/llmentary/compare/v0.1.0...v0.2.0) (2026-10-20)


### Features

* DOG-1 — path-prefix session attribution and pass-through forwarding ([#18](https://github.com/barzoj-co/llmentary/issues/18)) ([91388e2](https://github.com/barzoj-co/llmentary/commit/91388e2e399295f564519ce37ffc5e034311116f))
* DOG-2 — control API list, wait min_calls, JSON errors, and version ([#20](https://github.com/barzoj-co/llmentary/issues/20)) ([0eae1f4](https://github.com/barzoj-co/llmentary/commit/0eae1f49945023b4cb3e4e5566114054ea4ad352))
* DOG-3 — spec validation WARN log per finding ([#17](https://github.com/barzoj-co/llmentary/issues/17)) ([61bfa5e](https://github.com/barzoj-co/llmentary/commit/61bfa5e6a60aa4e97e50765fc07c32fa98cb5885))


### Bug Fixes

* DOG-5 — default OpenAI upstream, overlap path join, and loop guard ([#15](https://github.com/barzoj-co/llmentary/issues/15)) ([b6237dc](https://github.com/barzoj-co/llmentary/commit/b6237dc17dc9d0c86cb9d4cc6d39e8fa82130392))
* DOG-9 — HTTP 4xx/5xx JSON errors are not mid_stream ([#19](https://github.com/barzoj-co/llmentary/issues/19)) ([f276aae](https://github.com/barzoj-co/llmentary/commit/f276aae9514d8b234aa7c36b3ab0b54691a0d8f9))

## 0.1.0 (2026-10-19)


### Features

* first release of llmentary proxy ([#12](https://github.com/barzoj-co/llmentary/issues/12)) ([14fb6d9](https://github.com/barzoj-co/llmentary/commit/14fb6d9a310eb9a52df68ad008bf0afecb1a24d2))
