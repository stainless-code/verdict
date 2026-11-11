---
"@stainless-code/verdict": minor
---

Mine SZZ candidates from a local clone. `backtest mine --since
<90d|12w|18mo|2y|ref> [--until <ref>] [--run <slug>]` blames each later
`fix:`/`Revert "..."` commit's deleted lines to the merged or squash PR that
introduced them and writes
`.verdict/backtest/<run>/{run.json,mine.json}` with zero model calls and zero
network. Candidates carry the PR head, base, reconstruction, merge/head dates,
and the union of anchored `path:line:kind` bugs; exclusions are counted by
reason and never refused. `mine.json` is timestamp-free, so a rerun over the
same window is byte-identical. The artifact is not citable: SZZ blame is a
hypothesis, and replay/score land in later slices. The verb is CLI-only
because replay will spend on model runs.
