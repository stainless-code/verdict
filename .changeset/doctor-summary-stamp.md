---
"@stainless-code/verdict": patch
---

`doctor` human output opens with a `doctor @ <utc>` run stamp and closes with
`Summary: <healthy|unhealthy> — N ok, M warn, K fail`, reusing the existing
health predicate and per-check rows. `--json` envelopes and exits are
byte-identical; no new checks, no persisted state.
