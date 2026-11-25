---
"@stainless-code/verdict": minor
---

**Breaking:** `run --agent` now defaults to two isolated whole-diff finders, each followed by a cross-check from the other. This roughly doubles agent spend per run. Use `--finders 1`, or set the global `defaults.finders: 1` (personal fallback `review.finders`), to keep the single-finder procedure; scoped finder counts are refused. `--print` and `--stage` require one finder and say so when they refuse. `drain` stays single-agent and `backtest` replays use one finder. A re-run moves the previous execution's artifacts to `x-superseded/<id>` before the new launch.
