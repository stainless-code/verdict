---
"@stainless-code/verdict": patch
---

`verdict next --coverage` with no positionals now leads with one `error:` line
that names what is missing, like the other verbs. A plain `Basis:` line directly
under the quoted `Confidence:` line is accepted without a `basis-replaced`
warning.
