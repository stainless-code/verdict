---
"@stainless-code/verdict": patch
---

`backtest mine` in a `--filter=blob:none` clone used to stall with no candidate
for as long as the network kept answering; it now refuses `partial-clone` and
says how to get the missing objects back.
