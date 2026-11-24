---
"@stainless-code/verdict": minor
---

Add per-repo monthly spend ceiling (`monthly_budget_usd` in `.verdict/config.json` `defaults` and `spend.monthly_budget_usd` in personal settings). Effective ceiling is the stricter of the two. `run` refuses `spend-capped` (or `budget-unknown` when `require_known_estimate` sees unknown cost) before launching any agent process when month-to-date recorded spend plus the pending run's projected cost would exceed it; `--consent-over-budget` consent is per-invocation only. `report --cost` now carries a month-to-date block (`mtd` in JSON, `mtd:` in human) with ceiling, remaining, and priced/unknown counts when a cap is set and stays byte-identical when unset.
