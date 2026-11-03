---
"@stainless-code/verdict": minor
---

The PR conversation is a local, schema'd record instead of something each verb
re-reads from the host. Every fetch writes `.review/<pr>/threads.json` (schema
published) holding each thread's host id, diff anchor, resolved and outdated
state read from the host's own fields, root author kind, and the comments with
authors, timestamps, and capped bodies; GitHub comes from GraphQL
`reviewThreads` (REST carries no resolution), and GitLab, GitCafe, Forgejo,
Bitbucket, and Azure fill the same shape, paging rather than truncating.
`verdict threads <owner/repo> <pr> [--provider …] [--json]` re-reads just that
surface and rewrites only that file, so re-review, address-reviews, readiness,
and calibration can join prior feedback on any of the six hosts. Posted inline
comments now carry a hidden final-line `<!-- verdict-finding sig=… -->` marker,
so a new comment links back to the finding that produced it while older
comments keep reading as unlinked.
