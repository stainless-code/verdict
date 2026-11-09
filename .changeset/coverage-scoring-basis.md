---
"@stainless-code/verdict": patch
---

The coverage read names what its percentage counts. `next --coverage` and the
`coverage-incomplete` refusal said only "% of changed lines attested", while the
evaluator scores every hunk span on both sides, context included — so a
reviewer who attested the diff read a lower number than they had covered. Both
read points now state the basis beside the number, for example `coverage: 54%
of full hunk spans, both sides and context (12/22 lines, 1 files)`. The `--json`
payload, the machine-built body line, and the coverage warning are unchanged.
