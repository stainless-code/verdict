---
"@stainless-code/verdict": minor
---

Score a replay, and run the whole backtest in one command. `backtest score --run
<id>` grades each replayed PR's recorded findings against the bug lines the
run's own SZZ candidates carry, through the corpus matcher (±1 line, one
finding per bug, text that names the defect kind), and writes
`.verdict/backtest/<run>/report.json`. It is a pure read: no agent launches, no
bundle is built, and nothing enters `.verdict/state/corpus-*`.
`catch_rate` is `matched / bugs` and is a lower bound on catch — a line a later
fix deleted is a hypothesis, so a defect fixed without touching the line is
invisible. `off_target_per_pr` is `(reported − matched) / prs`; an unmatched
finding is not proven false, so the report never calls it one.
`cost_per_review_usd` is null when nothing was priced, and
`spend.unpriced_reviews` marks a partial total as a lower bound. A per-signal
table splits the rate by `fix` and `revert`, `unclassified` counts bugs no
finding can name, and every zero denominator renders `null`. `backtest mine`
now records each bug's seed `signal` in `mine.json`. Bare `backtest --since
<window> --agent <name>` composes mine, replay, and score on one run id. The
score report is also the verb's `--json` data, published as
`schemas/backtest.schema.json`. Scores are not citable.
