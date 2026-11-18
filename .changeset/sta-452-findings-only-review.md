---
"@stainless-code/verdict": minor
---

`review.json` is now a findings-only handoff: each finding carries a path, strict integer line, severity, claim, quoted code, and finder confidence; optional spec checks and receipts reserve the evidence S4 verifies. Verdict renders the body and derives approval from the stored findings, with a documented default threshold and the existing block-clause policy. The document keeps `"schema_version": "1"` — this is a breaking change inside that version, shipped with no shim.

**Breaking changes**

`review.json` must carry `findings[]` and may no longer carry `approved`, `body`, `comments[]`, `sections[]` or a top-level `confidence`; the document is closed, so any of those keys refuses as an unknown field and there is no repair hint for the old shape. To rewrite a stored v1 file, move each `comments[].body` into `findings[].claim`, add `quote`, `severity` and `confidence` to every finding, convert `line` to an integer, move the old `body` prose to `summary` (at most 2000 characters), drop `approved`, `sections[]` and top-level `confidence`, and keep the claim arrays (`resolved`, `triage`, `coverage`, `chunks`, `suppressed`, `risk`, `security_summary`) unchanged.

One problem code that policed the old contract is gone: `basis-replaced` (the agent writes no
body, so there is no line for the CLI to repair). The closed document refuses the removed keys
as `unknown-field`, and the review prompt drops `in-bounds` with the rest of the body grammar.

The dry run's `data.check` now reports `findings` and `anchors_moved_to_body` in place of the
`comments` and `sections` counters that described the old body grammar.

Approval is no longer an agent claim. With no repository policy, any open, visible finding whose quote standing is not `unverified` at `important` or above withholds approval, as does any violated `spec_checks[]` entry; `block_clauses` and `report --check` refine that default when configured. `record` decides once and stores the decision in `record.json` as `approval`, beside the `review_sha256` it decided on; `post` publishes that stored decision, renders its body from the same decision, and refuses `record-stale` when the review, diff, or head moved after the record, rather than deciding again over a review nobody recorded.

Do not release between this slice and S4. S1 stores `spec_checks[]` and `receipts[]` but does not verify quotes or require receipt coverage; S4 adds the insufficient-review refusal and closes the gap where a findings-free review would otherwise derive approval. The release hold is tracked in Linear.
