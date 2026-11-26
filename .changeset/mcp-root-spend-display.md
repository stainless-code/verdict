---
"@stainless-code/verdict": patch
---

The MCP `query-*` tools and `verdict://` dashboard resources now serve the repository the server runs in, as `acp` does, instead of the directory the binary is installed in. Before, every installed binary answered them with no bundles.

Measured spend is now shown on every read surface. `report --cost` adds a `measured:` line (`measured_usd` and `measured_receipts` in JSON) for the spend receipts a two-finder run settles. Its `receipts:` line counts only real `--consent-over-budget` runs, and a token count it cannot know prints `n/a` instead of `null`. The publish footer prices a two-finder bundle from that measured spend, and the `fetch` estimate treats each settled receipt as a comparable run. Money prints rounded to four decimals, so an agent's float noise (`$1.30604299999999994`) no longer shows. The `spend-capped` refusal names the setting to raise: `spend.budget_usd` for the per-run ceiling, or `spend.monthly_budget_usd` and `defaults.monthly_budget_usd` for the monthly one.

`run --agent agy` no longer warns about print-mode permissions when the arguments after `--` already carry `--dangerously-skip-permissions`.
