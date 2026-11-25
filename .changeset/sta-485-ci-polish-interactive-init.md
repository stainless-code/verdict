---
"@stainless-code/verdict": minor
---

`verdict init --ci github --comment-trigger` adds a `/verdict` comment re-run. The workflow also runs on `issue_comment`, but only for a pull-request comment from an `OWNER`, `MEMBER` or `COLLABORATOR` that is exactly `/verdict` or starts with `/verdict `. Such a comment replaces the review in flight for that pull request; other comments cancel nothing. It never adds `pull_request_target`, never checks out the pull request head, and never puts the comment body into a script. The flag is GitHub-only.

Bare `verdict init` on a terminal now asks three questions: the preset, the CI workflow (defaulted from the `origin` host) and the agent (defaulted to the first installed one). Enter keeps each default, and the run writes the same files the explicit flags would. Any selection flag, `--json`, or redirected input or output keeps it non-interactive. The new `init` flags reach the Go SDK's `InitRequest`.
