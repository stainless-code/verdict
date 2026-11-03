---
"@stainless-code/verdict": minor
---

Benchmark corpus with measured review rates. A marked `corpus-*` bundle kind
carries a checked-in case manifest (`schemas/corpus.schema.json`) with
ground-truth bugs and provenance; `calibrate` grades its findings offline
against that ground truth and reports precision, recall, and false-positive
rate, and `trend` aggregates the same numbers per model and panel. Ordinary
`local-*` reviews keep refusing the live join. Ships a seeded starter corpus
under `bench/corpus/` and a `docs/benchmark.md` operator runbook for real PRs,
real agents, cost estimates, and publishing measured rates.
