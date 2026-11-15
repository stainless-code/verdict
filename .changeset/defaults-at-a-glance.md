---
"@stainless-code/verdict": patch
---

`docs/manual.md` gains a "Defaults at a glance" table: one row per default a
bare run takes, naming the verb each one shapes. The table is generated from
the typed defaults registry and the flag parsers, so it cannot drift from the
shipped binary.
