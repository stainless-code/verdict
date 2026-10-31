---
"@stainless-code/verdict": patch
---

Basis-line validation compares the agent's line against the review's own comments instead of the merged findings store, so re-reviews with carried-over findings no longer warn. Genuine miscounts still warn and keep the body.
