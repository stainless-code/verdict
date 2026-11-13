---
"@stainless-code/verdict": minor
---

Add bounded foreground GitHub PR waits through `pr-wait`, MCP `pr_wait`, and
Go and TypeScript clients. Conditions cover merge, closure, checks and review
changes. Timeouts return typed data. `ready --wait-for build --timeout D` waits
for CI completion and returns not-ready on timeout. GitLab and Forgejo waits
refuse explicitly; no daemon or saved cursor is introduced.
