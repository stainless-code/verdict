---
"@stainless-code/verdict": patch
---

`report --cost` keeps a chunk's spend when the bundle's own `cost.json` is unreadable. The broken top-level record used to be folded into the same rollup as the per-chunk records, so one bad file voided every chunk beside it and reported zero tokens for a run that had real measured spend. It is now dropped on its own — named once by `cost.json unreadable — spend unrecorded for this rollup`, the same way an unreadable chunk is named — and the chunk records still price. No other shape changes: a bundle with no chunk records reads its top-level record exactly as before.
