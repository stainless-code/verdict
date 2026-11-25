---
"@stainless-code/verdict": patch
---

`gate` now fails on a violated spec check. The always-on `spec` dimension reads the spec checks the stored record graded: a `violated` one fails the gate with `violated spec check: <item>` and a tally, while `met`, `n/a` and a review with no spec checks pass. Before, a bundle whose only defect was a violated spec check passed the gate even though approval was withheld.
