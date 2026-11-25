---
"@stainless-code/verdict": patch
---

`verdict panel` now reads its verifier prompt from the binary's embedded Procedure instead of a `procedure/` folder beside the executable, so it works from an installed binary and not only inside a verdict checkout. `verdict init` presets now write panel rosters `verdict panel` accepts (`standard` was a single `main` entry, which panel refuses for lacking a verifier; it is now `lead`, `lens-1`), and the budget guard grows with strictness: `chill` `0.10`, `standard` `0.50`, `strict` `2.00` (it was inverted, so the deepest panel carried the tightest guard).
