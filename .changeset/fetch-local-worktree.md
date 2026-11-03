"@stainless-code/verdict": patch
---

Isolated local reviews: `fetch-local --worktree` checks the review target
into a temporary detached worktree and builds the bundle from that checkout
instead of the live tree, so caller tracing and verification run against
real, stable code without disturbing the working tree. The checkout is
always removed before the verb exits (success, refusal, or cancel); when no
checkout can be created the verb refuses typed `worktree-failed` and writes
nothing. Without the flag every byte behaves exactly as before.
