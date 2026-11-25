---
"@stainless-code/verdict": minor
---

`verdict serve` adds a diff view with word-level highlights, grouped by change lane. `GET /v1/roots/{root}/bundles/{bundle}/diff` serves the bundle's diff as side-by-side and unified tables with changed words highlighted, unchanged runs collapsed, findings anchored on their exact line and side, and lanes in risk order (`schema`, `source`, `config`, `tests`, `docs`, `generated`, `lockfiles`, `renamed`, `whitespace`). The UI gains a Diff tab, and the static `report --html` export includes the diff.
