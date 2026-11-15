---
"@stainless-code/verdict": patch
---

Publish "Defaults at a glance" in `docs/manual.md`: one row per default a bare
run takes (the typed defaults floor and the flag postures `gate`, `post` and
`run` start from), each naming its verb. The table is generated from the
resolved built-in layer and the parsers' own constants, and `internal/archtest`
fails when either moves without the docs, so it cannot drift into prose the
binary does not follow.
