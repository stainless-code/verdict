---
"@stainless-code/verdict": patch
---

The spend budget gate now prices comparable runs from usage records the operator explicitly hands over via `VERDICT_USAGE=<file|dir>`, in the new contract shape published as `schemas/usage.schema.json` (schema, model, tokens, optional USD, free-form source, window) — with the self-reported `cost.json` as the fallback. A handed record is joined by its window bracketing the cost record's mtime; a named path that is missing or malformed refuses typed (`bad-usage`) before the bundle is written. Automatic discovery of Claude Code transcripts and opencode exports was removed (it never shipped in a release); the optional converters live outside the binary in `examples/usage-adapters/`. `fetch`/`fetch-local` `data.estimate` keeps the additive `basis` (`measured`/`mixed`), `harness[]` (now the record `source` strings), and `measured_runs` keys plus the matching human/refusal note; with nothing handed over the output is byte-identical to before.
