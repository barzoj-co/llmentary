# Client assertion contract

This document is the wire contract for `AssertNoSpecViolations` and
`AssertNoIgnoredFields` in llmentary-go, llmentary-python, and
llmentary-js. Implementations live in those repositories. This repo only
defines the semantics and pins them with a reference checker in
`tests/proxy` (`TestD9aClientContract`).

Do not add these APIs to `pkg/llmentary` here (the clients live in their own repositories).

The Go client reads `trace.Calls[i].Normalized.Validation`, `.SpecViolations`,
and `.IgnoredFields`. Field names in `docs/data-model.md` are the wire names.

## `AssertNoSpecViolations(opts...)`

- Fails if any call in scope has a non-empty `spec_violations`.
- **Also fails with a distinct message if validation was not run**: `validation`
  is absent, `mode:"off"`, or `request_checked:false`, unless `AllowUnvalidated()`
  is passed. It never passes vacuously.
- Options: `Side(request|response)` (default request), `Calls(filter)`,
  `IgnorePaths(prefixes…)`, `IgnoreKeywords(…)`.

## `AssertNoIgnoredFields(opts...)`

- The same "never vacuous" rule.
- Default scope: `severity:"warning"`.
- Options: `IncludeInfo()`, `IgnoreKinds(…)`, `IgnorePaths(…)`, `Calls(filter)`.

## Failure message

One line per finding:

```
<call#> <api_family> <side> <path>: <message|kind> — <hint>
```

Against an `off`-mode or missing-`validation` trace, the message must include
`validation was not run`.
