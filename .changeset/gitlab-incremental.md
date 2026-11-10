---
"@stainless-code/verdict": minor
---

Incremental re-review on GitLab. `fetch --provider gitlab --base-sha <sha>`
(or `--since`) calls `glab api
projects/<id>/repository/compare?from=<base>&to=<head>`, joins the response's
per-file `diff` entries into `incremental.diff`, and names the base in
`incremental_base` — the same artifact, note, and `.compare.err` contract the
GitHub compare path already writes. A failed or unreadable compare still
finishes the fetch and writes `incremental.diff.note` instead of refusing.
Hosts without a compare adapter (GitCafe, Forgejo, Bitbucket, Azure) now accept
`--base-sha`/`--since` and write the note naming the unsupported provider
rather than refusing the fetch. The delta feeds the ordinary record/advance
path, so a second review round reopens delta-touched findings and records at
the fetched head.
