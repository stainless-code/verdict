---
"@stainless-code/verdict": patch
---

`NOTICE.md` now carries the full MIT license text of the upstream review
prompts Verdict adapts, pinned to the commit they came from, instead of a link.
The registry skill (`npx skills add stainless-code/verdict`) ships its own
`NOTICE.md` beside the adapted prompts, which it previously lacked. The skill's
trigger text now names every review target — a pull request, a branch, a commit
range or staged changes — so an agent picks it for local reviews too.
