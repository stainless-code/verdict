---
"@stainless-code/verdict": minor
---

Add `threads resolve <owner/repo> <pr> [--thread <id>]... [--dry-run]`, the
receipt-backed thread mutation: without `--thread` it closes every open
self-authored thread whose linked finding the local store marks addressed;
with `--thread` it closes only the named ids, and a human-authored thread
refuses. A thread is resolvable only when its root carries verdict's marker
and its root author equals the authenticated token user (read once per run;
an unreadable login refuses before any write); a forged marker skips as
`not-self-authored` by default and refuses `not-verdict-authored` when named.
`--dry-run` lists the plan and makes zero writes; a real run makes one
host write per thread and records `threads-resolve.json`. GitHub resolves
through `resolveReviewThread`, GitLab through the discussion resolved flag, and
`thread_resolution` now reads true on both; Forgejo refuses typed because its
API has no documented resolve endpoint.
