---
"@stainless-code/verdict": minor
---

`post` publishes to Forgejo pull requests over the REST API: one atomic review
carrying the body, the verdict, the reviewed commit, and every inline finding,
with `APPROVED` for an approval. A review comment anchors on a file line
number (`new_position` on the new side, `old_position` on the old), so a
finding with no numeric line is withheld, a LEFT anchor on a file the change
deletes is withheld, and a multi-line range lands on its end line; each loss is
counted in the envelope `warnings`. A 200 that answers `PENDING` refuses
`post-failed` instead of recording a receipt for a draft nobody sees, and a
pre-existing draft authored by the token user refuses with a submit-or-discard
hint. `data.capabilities` now reports Forgejo's post, inline anchors, and both
binding verdicts.
