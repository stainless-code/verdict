---
"@stainless-code/verdict": minor
---

`run --agent` now offers 32 more cited headless agents, including Cline and Junie after CLI-reference checks. Rows whose binary shares a name with another tool run a harmless identity probe before launch; a non-zero exit, a five-second timeout, or a missing match refuses the new `agent-mismatch` code before any agent starts, and `launcher.json` records `probe: {args, ok}` for a passing check. Operator agents can set `probe_args` and `probe_match` under settings.json `agents`, with `probe_match` required when `probe_args` is present.
