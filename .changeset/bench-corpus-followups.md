---
"@stainless-code/verdict": patch
---

A corpus bundle whose `full.diff` is prose rather than a diff no longer grades
leniently — it refuses, and `backtest score` names `backtest replay` when the
broken bundle is a replay bundle. The cohort skip warning now names only a
sibling that belonged to the cohort.
