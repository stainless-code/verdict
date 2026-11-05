---
"@stainless-code/verdict": minor
---

Findings can now carry a closed category (`logic`, `edge-case`, `security`,
`performance`, `accidental-commit`, `style`, `docs`, `other`): an optional
per-comment label that never changes the finding's signature. `report
--category` filters to one class, `report --check --category` gates only that
class, and `report`, `calibrate`, and `trend` all publish per-category counts
(untagged for unlabelled findings). Unknown or non-string values warn once
and store null; they never refuse a review.
