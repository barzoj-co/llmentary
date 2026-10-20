# Changelog

## Unreleased

### Bug Fixes

* `GET /sessions/{id}/wait` on a session that does not exist yet returns `504`
  `wait_timeout` after `quiet` when `min_calls=0`, instead of `200` with an
  empty trace. Unknown-id `GET`s no longer create phantom sessions.

## 0.1.0 (2026-10-19)


### Features

* first release of llmentary proxy ([#12](https://github.com/barzoj-co/llmentary/issues/12)) ([14fb6d9](https://github.com/barzoj-co/llmentary/commit/14fb6d9a310eb9a52df68ad008bf0afecb1a24d2))
