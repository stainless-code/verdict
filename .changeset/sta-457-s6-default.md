---
"@stainless-code/verdict": minor
---

`run --agent` now defaults to two isolated whole-diff finders followed by opposite-finder cross-checks. Global `defaults.finders` accepts 1 or 2; personal `review.finders` supplies a fallback, and `run --finders` overrides the captured value. Scoped finder counts are refused.

Use `--finders 1` for the existing single-finder procedure, repair loop, and artifact contract. `--print` and `--stage` require one finder and name that remedy when they refuse. Drain remains single-agent, and backtest replay explicitly retains one finder. MCP and SDK surfaces consume the resulting evidence without gaining a launch method.

`config.schema.json` adds the global finder count. Configuration evidence and generated goldens now include its resolved value and origin. Two-finder execution receipts record the selected count and source before the first launch.

Reruns preserve previous automatic execution artifacts under `x-superseded/<id>`. Retirement publishes before a replacement launch; a failure refuses the launch. Captured execution identity stays active until retirement completes, so an interrupted move cannot admit stale confirmations. A single-finder replacement ignores retired cross standing while retaining finding history.
