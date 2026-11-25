---
"@stainless-code/verdict": minor
---

`verdict serve` now offers a read-only compare view alongside the detail view: `GET /v1/roots/{root}/bundles/{bundle}/compare?baseline={bundle}` serves the same four-bucket comparison `report --compare --json` prints, and a baseline that is missing, malformed, or physically outside the served root refuses typed (`bad-baseline`, `missing-compare-bundle`) instead of reading another tree. The review detail page grows a **Compare** tab with a same-root baseline picker, and each finding row offers one-click **Copy prompt** and **Copy triage command** buttons served by `GET /v1/roots/{root}/bundles/{bundle}/findings/{sig}/copy` — the copied triage line always meets the service's 30-rune rationale floor, so it runs as pasted without `--force`. Static HTML exports omit the Compare tab they cannot serve.
