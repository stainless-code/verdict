---
"@stainless-code/verdict": minor
---

**Measurement**

- **Clean controls** — the benchmark corpus accepts clean controls with empty ground truth, ships two seeded clean cases, and keeps seeded fixtures labelled as not a model-quality claim. [Benchmark corpus](docs/benchmark.md) <!-- changesets: benchmark-clean-controls.md -->
- **Reproducible metrics** — typed severity weights, ordered k-of-n recall, control false-positive rates, weighted F1, signal-to-noise, and explicit denominators, with provenance and argv recorded. [Benchmark corpus](docs/benchmark.md) <!-- changesets: benchmark-controls-metrics.md -->
- **Adjudicated rates** — a versioned per-run adjudication record reclassifies an unmatched finding as `real-unlisted`, `matches-ground-truth`, or `false-positive`, and `bench/runner -render` recomputes strict and adjudicated precision and recall without re-running a model. [Benchmark corpus](docs/benchmark.md) <!-- changesets: bench-adjudication.md -->
- **First real cases** — four real cases from `urfave/cli` (three bugs and one clean control), `-max-usd` stops scheduling at measured spend and marks the run uncitable, and refused samples are counted in the cost rollup. [Benchmark corpus](docs/benchmark.md) <!-- changesets: bench-real-pilot.md -->
- **Corpus grading guard** — a keyword the case's own diff spells refuses, a case left with no phrase asks for another keyword, and a real bundle whose `full.diff` is missing, unreadable, or prose is refused instead of graded leniently. [Benchmark corpus](docs/benchmark.md) <!-- changesets: bench-keyword-guard.md, bench-corpus-followups.md -->
