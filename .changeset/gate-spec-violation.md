---
"@stainless-code/verdict": patch
---

`report --check` and `gate` now fail on a violated spec check. The new always-on `spec` dimension reads the spec checks the stored record graded: a `violated` one fails the gate with `violated spec check: <item>` and a tally of every violated item, while `met`, `n/a`, and a review with no spec checks pass. Before this, a bundle whose only defect was a violated spec check passed the gate while the derived approval withheld with `violated spec check`, so the gate certified a review the decision refused.
