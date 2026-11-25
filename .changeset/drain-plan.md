---
"@stainless-code/verdict": minor
---

New `verdict drain` answers the review threads waiting on a reply. It refreshes the threads, plans the ones that need a reply, and pins the plan with a digest. `drain --agent <name>` launches one agent through the same launcher as `run` and checks its `drain.replies.json` against the plan.
