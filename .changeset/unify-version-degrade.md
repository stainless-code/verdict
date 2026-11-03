---
"@stainless-code/verdict": patch
---

Builds with no release metadata now report one degrade string everywhere:
`--version`, the skill payload and receipt, and the `report --output`/posted
footer all say `0.0.0-unknown` (the footer previously said `unknown`).
