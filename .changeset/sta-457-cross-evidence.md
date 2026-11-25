---
"@stainless-code/verdict": minor
---

**Breaking:** cross-check (panel) receipts are now version 2. verdict assigns each checker its identity, and a receipt binds the execution, head, diff, frozen review and dispatch plan. Every confirming or refuting quote must pass the same quote check as a review finding. Approval, `post`, `report`, `gate` and `ready` require every assigned cross-check, and a missing or disputed check on a blocking finding withholds approval as insufficient. Manual panel decisions are unchanged when no two-finder execution was captured. Version 1 panel receipts are refused: re-run `verdict panel` to write v2 receipts.
