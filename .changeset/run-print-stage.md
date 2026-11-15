---
"@stainless-code/verdict": minor
---

Render one recipe stage at a time with `run --print --stage <n> --recipe
<slug>`. `n` is 1-based over the recipe's four stages (`1 lenses`, `2
synthesize`, `3 dedupe`, `4 rank`); the rendered argv is the agent's own
flags, the stage's declared model hint as that agent's model argument, your
`--` arguments, and a directive naming the stage, its hints and caps, and the
`review.json` it reads and writes. Every stage reads the bundle and writes
`review.json`, so stages chain through the artifact `record` and `post` already
consume and your CI can dispatch them under different models. Verdict still
launches nothing and sequences nothing: `--stage` requires `--print` and
`--recipe` (`bad-flags` otherwise, as does an index outside 1–4 or a
pass-through `--model`/`-m` against a declared hint), `--json` carries
`data.stage` (`{"index", "kind"}`), and plain `--print` is unchanged.
