---
"@stainless-code/verdict": minor
---

Operator-owned template overrides: per-block fragments under `.review/overrides/` (preamble, footer
order, walkthrough threshold, attribution) guarded by an enumerated locked-invariant list (marker line,
findings structure, guard satisfiability, metadata presence, divider shape), validated at check-tree time
and revalidated at post time with typed refusals. `check-tree --json` emits the standard envelope on all
paths.
