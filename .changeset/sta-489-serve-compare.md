---
"@stainless-code/verdict": minor
---

`verdict serve` adds a read-only compare view: `GET /v1/roots/{root}/bundles/{bundle}/compare?baseline={bundle}` serves the same comparison as `report --compare --json`, and a missing, malformed or out-of-root baseline refuses (`bad-baseline`, `missing-compare-bundle`). The detail page gains a **Compare** tab with a baseline picker, and each finding offers **Copy prompt** and **Copy triage command** buttons; the copied triage command runs as pasted. Static exports omit the Compare tab.
