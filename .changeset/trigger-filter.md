---
"@stainless-code/verdict": minor
---

`ready --filter` now gates the review on the closed trigger predicates in the trusted base configuration: matching PRs run and record the decision, non-matching PRs refuse `filtered-out` before any host call, and unusable policy or facts refuse `trigger-failed`.

Fetch now requests and records PR labels in `PR.json` for every host, so the `labels` trigger predicate can read them from the bundle.
