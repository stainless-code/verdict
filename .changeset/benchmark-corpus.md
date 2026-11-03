---
"@stainless-code/verdict": minor
---

Measured review rates instead of impressions. A marked `corpus-*` bundle kind
carries a checked-in case manifest with ground-truth bugs and provenance;
`calibrate` grades a model's findings against that ground truth offline and
reports precision, recall, and false-positive rate, and `trend` aggregates the
same numbers per model and panel. Ordinary `local-*` reviews keep refusing the
live join. Ships a seeded starter corpus under `bench/corpus/` and
`docs/benchmark.md`, an operator runbook for real PRs, real agents, cost
estimates, and publishing measured rates.
