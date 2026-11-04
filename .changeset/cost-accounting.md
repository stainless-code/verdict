---
"@stainless-code/verdict": patch
---

Cost accounting reads what the operator actually knows. `cost.json` entries
gain optional `cache_read_input_tokens` and `cache_creation_input_tokens`
(subsets of `input_tokens`; absent or `null` reads unknown), and
`report --cost` rolls them into a top-level `cached_input_tokens` scalar with
totals unchanged. The spend gate can now price comparable runs from usage
records the operator hands over with `VERDICT_USAGE=<file|dir>`, in a published
schema (schema, model, tokens, optional USD, source, window), with the
self-reported `cost.json` as the fallback; a handed record joins by its window
bracketing the run, and a named path that is missing or malformed refuses typed
before the bundle is written. Automatic discovery of other tools' transcripts
is gone — the optional converters live outside the binary in
`examples/usage-adapters/`. With nothing handed over, output is byte-identical.
