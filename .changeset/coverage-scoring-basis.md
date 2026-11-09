---
"@stainless-code/verdict": patch
---

The coverage read names what its percentage counts. `next --coverage` and the
`coverage-incomplete` refusal said only "% of changed lines attested", while the
evaluator scores every hunk span on both sides, context included, so a reviewer
who attested every changed line but not its hunk context read a lower number
than they had covered. Both read points now state the basis beside the number,
for example `coverage: 54% of full hunk spans, both sides and context (12/22
lines, 1 files)`. The record warning stderr line uses the same basis, and the
constant lives once in `internal/core/coverage` so the surfaces cannot drift.
The `--json` keys are unchanged; the `warnings` string now names the same
basis. The machine-built body line is unchanged.
