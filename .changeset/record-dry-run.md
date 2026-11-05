---
"@stainless-code/verdict": minor
---

`verdict record` can now answer "would this record?" before writing anything.
`record --dry-run` runs the same checks and writes nothing, returning a
summary of the comments, sections, and anchors that would move to the body.
Both modes share one checker instead of stopping at the first problem: the
refusal carries `error.problems[]` in document order with a JSONPath, a
published `code`, the level, the message, the exact `fix`, and an optional
`example`, plus `error.problems_truncated` past the 50-problem cap; human mode
lists the same problems before its try/re-enter lines. The checker owns the
brief, anchor/side, kb-id, chunk-attestation, confidence, security, claim,
triage, section, and verification-input checks, not just the top-level shape.
Warnings (moved anchors, brief parts dropped without refusing, malformed kb
ids, unknown fields, an unset rigor) let the record proceed unless `--strict`
or `"strict_input": true` in `controls.json` promotes them.
`pkg/verdict.CheckReview` exposes the pure checker, the MCP `record` tool gains
`dry_run` (served on a read-only server; a non-dry record is still refused),
and the review skill now loops write -> dry-run -> fix for at most three
rounds, then reports any remaining problems instead of recording a partial
review.
