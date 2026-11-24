---
"@stainless-code/verdict": minor
---

`verdict pr-wait` (and `ready --wait-for build`) now work on GitLab merge requests, reusing the `glab` reads `ready` already makes: state, head, the head pipeline as the check rollup, and, for `review-changed`, approvals and discussions. One per-host capability table now drives `ready` and `pr-wait` and is the single source of the advertised `--provider` list (CLI help, the MCP `pr_wait` schema enum) and of the refusal, so advertising can no longer drift from what is built. Forgejo is no longer advertised for `pr-wait` — it was never implemented, and `--provider forgejo` (or any host outside the table) now refuses `provider-unimplemented` up front, naming the supported hosts (github, gitlab), instead of failing inside the first poll.

GitLab waits count the head pipeline only when it ran for the current source head (a merged-results or merge-train pipeline qualifies when its merge commit lists that head as a parent), compare approvals and discussions without regard to API order, treat a `locked` merge request as open instead of closed (`ready` no longer reports it `CLOSED` either), and keep `glab`'s own error in a refused read. The `pr-read-failed` hint now names `glab auth status` for GitLab, the `ready` MCP tool description names the hosts the table serves, and the embedded command reference no longer says GitLab waits refuse.
