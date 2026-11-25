---
"@stainless-code/verdict": minor
---

**Breaking:** `review.json` is a findings-only handoff. Each finding carries `path`, an integer `line`, `severity`, `claim`, the quoted code (`quote`) and the finder's `confidence`; optional `spec_checks[]` and `receipts[]` carry the evidence verdict checks. `approved`, `body`, `comments[]`, `sections[]` and a top-level `confidence` are refused as unknown fields. The document keeps `"schema_version": "1"`, with no shim.

To rewrite a stored review: move each `comments[].body` to `findings[].claim`, add `quote`, `severity` and `confidence`, make `line` an integer, move the old `body` to `summary` (at most 2,000 characters), and drop `approved`, `sections[]` and the top-level `confidence`. The claim arrays (`resolved`, `triage`, `coverage`, `chunks`, `suppressed`, `risk`, `security_summary`) are unchanged. The `basis-replaced` problem code is gone, and the dry run's `data.check` reports `findings` and `anchors_moved_to_body` instead of `comments` and `sections`.

Approval is no longer an agent claim. By default, any open, visible finding at `important` or above whose quote is not `unverified`, or any violated spec check, withholds approval; a repository's `block_clauses` refine that. `record` decides once and stores the decision in `record.json` as `approval`. `post` publishes that decision and refuses `record-stale` if the review, diff or head moved since.
