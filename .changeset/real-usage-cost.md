---
"@stainless-code/verdict": patch
---

The spend budget gate now prices comparable runs from local agent-harness session files when one is available — Claude Code transcripts auto-discovered under `CLAUDE_CONFIG_DIR`/`~/.claude`, and opencode `export` JSON named by `VERDICT_HARNESS_USAGE` — with the self-reported `cost.json` as the fallback. A measured session is joined by its window bracketing the cost record's mtime, so unrelated sessions never price a review. `fetch`/`fetch-local` `data.estimate` gains additive `basis` (`measured`/`mixed`), `harness[]`, and `measured_runs` keys plus a matching human/refusal note; with no harness data the output is byte-identical to today. Local-only: no network, and tokens without a priced total are never priced by the tool.
