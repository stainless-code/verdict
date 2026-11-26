---
"@stainless-code/verdict": minor
---

**Breaking:** verdict no longer reads bundles written by older versions; it refuses them with the new `bundle-outdated` code, whose message names the fix, instead of degrading.

- A `run.json` without `requires_evidence` and `inputs_sha256` (written before the integrity binding) refuses on every read: `post`, `record`, `report`, `ready`, `triage`, `verify --group`, `config --explain`, and `run`. It was previously enforced as unverified policy with a "pre-integrity bundle" warning.
- `config --preview` on a bundle directory without `run.json` refuses instead of guessing the file list from `.difffiles`. With no bundle directory at all it still previews.
- A `findings.json` whose `verification` is the bare array of panel results (0.7.x) refuses instead of having its panel read.
- A corpus bundle without `corpus_run.json` refuses in `calibrate` and `trend` instead of scoring as an independent k=1 sample. `bench/runner`'s loop mode now writes `corpus_run.json` for its final bundle too.

Migration: re-fetch bundles (`verdict fetch`, `verdict fetch-local`) and re-run reviews and benchmarks created before 0.8.0.
