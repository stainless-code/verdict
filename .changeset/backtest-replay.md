---
"@stainless-code/verdict": minor
---

Replay mined candidates against your own agent. `backtest replay --run <id>
--agent <name> [--max-reviews <n>] [--budget-usd <usd>] [--timeout <secs>]
[--rounds <n>] [-- <agent args>]` walks the run's `mine.json` newest merge
first, builds a fresh local bundle at each candidate's own `base..head`,
launches the agent in a detached `git worktree` of that head (removed again
after each review, failures included), and records the review into the
bundle's `findings.json`. Spend is bounded by `--max-reviews` (1–99), a
median-projected `--budget-usd` ceiling, and a per-review `--timeout`; a review
that reported no price is unknown spend, never `$0.00`. A failed review is
that candidate's row (`status` is the refusal code) and the walk continues;
only `agent-missing`, `agent-spawn-failed`, cancellation, and a
`backtest-failed` that would hit every candidate (git gone mid-run, or a
state directory that cannot be created) end the run.
`--run` ids are capped at 42 bytes by `backtest mine` and `backtest replay`
alike, so two runs can never be cut onto one bundle name.
Rerunning the same `--run` replays only candidates that produced no review, and
a stored candidate replayed with another agent or other pass-through arguments
refuses `run-mismatch`. Every bundle carries a `backtest.json` marker, so
`trend` keeps replay input out of your history. Artifacts:
`.verdict/backtest/<run>/prs/<NNN>/replay.json`. Replays are not citable.
