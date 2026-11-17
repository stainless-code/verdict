---
"@stainless-code/verdict": minor
---

`drain` is a hidden CLI verb that refreshes threads, plans the review threads waiting on a verdict reply, and pins the plan with a digest. `drain --agent <name>` launches one agent through the same launcher `run` uses and validates its `drain.replies.json` against the plan. No host writes or reply posting ship in this slice.
