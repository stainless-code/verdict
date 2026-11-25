---
"@stainless-code/verdict": minor
---

`verdict serve` now renders a review diff view with word-level highlights, grouped by change lane. Add `GET /v1/roots/{root}/bundles/{bundle}/diff` serving the bundle `full.diff` as side-by-side and unified tables with changed words highlighted from server-computed spans, collapsed unchanged runs, findings anchored on their exact path, line, and side, and lanes grouped in risk order (`schema`, `source`, `config`, `tests`, `docs`, `generated`, `lockfiles`, `renamed`, `whitespace`). The engine is pure-Go histogram with Myers fallback and an indent heuristic, plus a word-level intraline pass. The embedded UI gains a Diff tab with the same toggle; the static `report --html` export snapshots the diff route in `data.json`.
