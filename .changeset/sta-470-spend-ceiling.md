---
"@stainless-code/verdict": minor
---

New per-repository monthly spend ceiling: `defaults.monthly_budget_usd` in `.verdict/config.json` and `spend.monthly_budget_usd` in personal settings, the stricter one winning. `run` refuses `spend-capped` (or `budget-unknown` when `require_known_estimate` sees unknown cost) before launching any agent when month-to-date spend plus the run's projected cost would exceed it. **Behavior change:** `--consent-over-budget` now covers only the current invocation. `report --cost` adds a month-to-date block (`mtd` in JSON) when a ceiling is set.
