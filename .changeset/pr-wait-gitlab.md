---
"@stainless-code/verdict": minor
---

`verdict pr-wait` (and `ready --wait-for build`) now work on GitLab merge requests, using the same `glab` reads as `ready`: state, head, the head pipeline as the check rollup, and approvals and discussions for `review-changed`. A GitLab wait counts the head pipeline only when it ran for the current head (merged-results and merge-train pipelines included), and a `locked` merge request reads as open, not closed.

One per-host table now drives `ready`, `pr-wait`, the advertised `--provider` list and the refusal. **Behavior change:** `pr-wait --provider forgejo`, which never worked, now refuses `provider-unimplemented` up front, naming the supported hosts (github, gitlab), instead of failing inside the first poll.
