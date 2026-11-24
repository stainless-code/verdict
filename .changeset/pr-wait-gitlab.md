---
"@stainless-code/verdict": minor
---

`verdict pr-wait` (and `ready --wait-for build`) now work on GitLab merge requests, reusing the `glab` reads `ready` already makes: state, head, the head pipeline as the check rollup, and, for `review-changed`, approvals and discussions. One per-host capability table now drives `ready` and `pr-wait` and is the single source of the advertised `--provider` list (CLI help, the MCP `pr_wait` schema enum) and of the refusal, so advertising can no longer drift from what is built. Forgejo is no longer advertised for `pr-wait` — it was never implemented, and `--provider forgejo` (or any host outside the table) now refuses `provider-unimplemented` up front, naming the supported hosts (github, gitlab), instead of failing inside the first poll.
