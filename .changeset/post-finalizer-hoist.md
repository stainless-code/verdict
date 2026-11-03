---
"@stainless-code/verdict": patch
---

`post` checks the local same-SHA finalizer before any network read: a repeat
post on an already-posted SHA refuses `already-posted` with zero `gh` calls,
and that deterministic local refusal wins over `approve-blocked` and
`duplicate-payload`. `--force` still overrides, unreadable/head-less records
still warn and proceed, and dry runs stay advisory.
