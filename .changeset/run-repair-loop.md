---
"@stainless-code/verdict": minor
---

`run` repairs an invalid `review.json` with `--rounds N` (1–5, default 1): round 1 sends today's directive unchanged, later rounds re-prompt the agent with the prior review bytes and the shape checker's fix list fenced under the run's nonce, and the loop stops on the first valid round. `--timeout` is per round, `--json` carries the loop in `data.repair`, and exhaustion refuses `repair-exhausted`.
