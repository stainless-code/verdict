---
"@stainless-code/verdict": minor
---

`verdict loop <owner/repo> --agent <name>` is a built-in agent self-review loop, replacing the Python recipe. Each round fetches your working tree into a fresh local bundle, runs a review-only agent pass, records it with `--require-coverage`, and gates every open finding. A failed gate starts a fix phase: the agent fixes the open findings, and `verdict verify` runs any command it declared in `patch.json`. The next round reviews the result from scratch.

The loop stops on `pass`, `max-rounds` (`--max-rounds`, default 3), `wall-budget` (`--budget`, default `600s`), `cost-budget` (`--max-cost-usd` reached, unmeasurable spend while it is set, or the monthly ceiling), `no-progress`, `agent-failed`, or another refusal. Every spawn leaves a spend receipt that the monthly total counts. `--sandbox` and `--sandbox-network-only` pass through to `verify`, `--fix-prompt <file>` replaces the fix instructions, and `--print` shows every planned command without running it. `round-N.json` and `summary.json` land in `--output`, and `--json` prints the summary.

**Breaking:** `docs/recipes/self-review.py` is removed; use `verdict loop`.
