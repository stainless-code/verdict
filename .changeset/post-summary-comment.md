---
"@stainless-code/verdict": minor
---

Add opt-in PR summary comment support via `post --summary comment|off` (default
`off`). When `off`, `post` output, host calls, and receipts stay byte-identical.
When `comment`, `post` maintains one separate PR-level summary comment carrying
a `<!-- verdict-summary head=<sha> -->` marker (distinct from review and
finding markers, never editing the PR description), ignoring comments by other
authors, updating the existing comment in place across GitHub (`PATCH`),
GitLab (`PUT`), and Forgejo (`PATCH`), linking to the submitted review and
posted inline comments where hosts supply URLs, and reporting duplicate matches
in the receipt without deleting older comments. `--dry-run` reports the planned
summary action (`create`, `update`, or `none`) without writing. A failed summary
write never loses the accepted review's receipt: `summary.action` is `failed`
with the reason, and the next post updates the summary.
