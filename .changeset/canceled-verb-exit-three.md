---
"@stainless-code/verdict": patch
---

A cancelled verb exits 3 everywhere. `ready`, `trend`, `threads`,
`calibrate`, `check-tree`, `doctor`, `report`, and `next`'s store-error leg
rendered a service `canceled` refusal through a renderer that hard-codes exit
1, so a SIGINT landing between the adapter's pre-check and the service call
reported an ordinary refusal. The cancel-to-3 mapping now lives in one place,
used by every refusal renderer, and every other refusal code keeps the exit it
had.
