---
"@stainless-code/verdict": minor
---

`fix --recipe <name>` now selects the findings a named recipe owns. `record` stores each finding's optional `lens` provenance, and the batch reads the recipe's lens set from the bundle's base snapshot, selecting only findings whose `lens` matches. A recipe that declares no lenses reports the empty scope instead of widening to every finding.
