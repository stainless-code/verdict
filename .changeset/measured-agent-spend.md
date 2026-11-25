---
"@stainless-code/verdict": minor
---

Verdict now measures agent spend from the agent CLI's own output instead of relying on self-reports. Every spawn that writes a spend receipt requests the CLI's structured output and reads the cost from it. That covers the self-review loop's review attempts and fix, and each launch of a two-finder `run`:

- `claude`: `--output-format json`, priced from `total_cost_usd`.
- `opencode`: `--format json`, the sum of each step's `cost`.
- `codex`: `--json`, the last turn's token usage priced from a dated, cited price table in the agent catalog. The model follows Codex's own precedence across `--model`, `-c` overrides, the selected profile (a `[profiles.<name>]` table or a `<name>.config.toml` file), and `config.toml`. Sources that disagree, or a model key verdict cannot read, leave the spawn unpriced. Local or non-OpenAI providers are never priced.

A spawn the CLI did not price falls back, in order, to the agent's self-report, then to the median of past measured spawns of the same agent and model (`projected`), and only then counts as unknown. A cost ceiling still stops on unknown spend. Each receipt records `priced_by` (`measured`, `tokens`, `agent`, `projected`, or `not-started`), `agent`, and `model`. A two-finder execution's spend receipt is settled to the measured total once every spawn was measured, so `loop --max-cost-usd` and the monthly ceiling count real spend.

Spend is read from the agent's stdout as it streams, never from the capped capture or from stderr. A truncated or malformed stream measures nothing. An older CLI that rejects the output flag is retried once without it.

A loop bundle is counted from its spawn receipts, so a measured spawn is never also counted from its self-report. Only a launch that never started is free. Two-finder receipts count once per invocation, and a receipt settled from measured spend outranks any later self-report.

A plain `run` and `demo` keep their argv unchanged. A review the agent prints inside the structured output is still salvaged.
