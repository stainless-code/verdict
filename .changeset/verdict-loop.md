---
"@stainless-code/verdict": minor
---

`verdict loop <owner/repo> --agent <name>` is the built-in agent self-review loop. It replaces the Python recipe and needs no Python. Each round fetches the working tree into a fresh local bundle, runs a review-only agent pass, records it with `--require-coverage`, and gates every open finding. A failed gate starts a fix phase: the agent fixes the open findings in the working tree, and `verdict verify` runs on any command it declares in `patch.json`. The next round re-fetches and reviews the fixes from scratch.

`--agent` takes any agent table row or `settings.json` agent. The loop stops on `pass`, `max-rounds` (`--max-rounds`, default 3), `wall-budget` (`--budget`, default `600s`), `cost-budget` (`--max-cost-usd` over the summed round costs, plus the monthly spend ceiling before every spawn), `no-progress`, `agent-failed`, or another typed refusal. It writes `round-N.json` and `summary.json` to `--output`. `--fix-prompt <file>` replaces the fix instructions, `--print` shows every planned command and prompt without running anything, and `--json` prints the summary as `$defs/loopData`.

`docs/recipes/self-review.py` is removed. `verdict demo` is unchanged: it now shares the loop's fetch → run → record pipeline.
