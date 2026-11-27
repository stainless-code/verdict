---
"@stainless-code/verdict": patch
---

A cost-capped `loop` or `run` now refuses typed `budget-unpriceable` before the first spawn when verdict cannot price the agent's launch. Before, it paid for round 1 and then stopped with `budget-unknown`. The caps are `loop --max-cost-usd`, or a monthly ceiling with `require_known_estimate` on `run`. verdict cannot price an agent whose CLI reports neither its cost nor token usage (`pi`), or `codex` with no model named, or one the price table lacks. The refusal names the fixes: a priced model (`-- -m <model>`, or `model` in codex's `config.toml`), an agent that reports its cost, or dropping the cap and bounding the loop with `--max-rounds` and `--timeout`. Declaring a price or `"free"` per model is planned (STA-527/STA-543). Without a cap, nothing changes.
