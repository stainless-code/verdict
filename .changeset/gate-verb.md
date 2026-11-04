---
"@stainless-code/verdict": minor
---

`verdict gate` is one composed CI gate over the three dimensions `report`
already computes: open findings at-or-above a tier (`--max-severity`), a
completed post for the fetched HEAD (`--require-head`), and live unresolved
threads (`--live auto|strict|skip`). One exit code carries the answer — 0 pass,
1 fail-or-unknown, 2 usage, 3 tool error — and the human tail names each
dimension, the tally, and one `try:` hint covering every failing dimension.
`--json` adds `data.passed` plus `data.dimensions[]` in `findings, head,
live_threads` order with `count` null where a dimension has no tally; a failed
gate keeps `error: null` because a verdict is not a refusal. `gate` is
read-only (no store writes, no post) and absent from MCP: a local pass is not
authority to merge.
