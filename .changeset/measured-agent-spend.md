---
"@stainless-code/verdict": minor
---

verdict now measures agent spend from the agent CLI's own output instead of the agent's self-report. Each spawn that writes a spend receipt (the self-review loop's review and fix, and each two-finder `run` launch) asks for structured output and reads the cost from it:

- `claude`: `--output-format json`, priced from `total_cost_usd`.
- `opencode`: `--format json`, the sum of each step's `cost`.
- `codex`: `--json`, the last turn's tokens priced from a dated, cited price table, for the model Codex itself would pick (`--model`, `-c`, the profile, `config.toml`). Conflicting sources, an unreadable model, or a local or non-OpenAI provider leave the spawn unpriced.

An unpriced spawn falls back to the agent's self-report, then to the median of past measured spawns of the same agent and model (`projected`), and only then counts as unknown. A cost ceiling still stops on unknown spend. Each receipt records `priced_by`, `agent` and `model`, and a two-finder receipt is settled to the measured total, so `loop --max-cost-usd` and the monthly ceiling count real spend. A truncated or malformed stream measures nothing, and an older CLI that rejects the output flag is retried once without it.
