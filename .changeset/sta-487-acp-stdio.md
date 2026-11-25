---
"@stainless-code/verdict": minor
---

New `verdict acp`: a lightweight ACP v1 stdio server for editors, read-only, with no model calls and no daemon. `initialize` negotiates protocol version 1, and four methods carry the reads: `verdict/bundles/list`, `verdict/findings/list` (with evidence rungs), `verdict/findings/get`, and `verdict/findings/copy`, which returns the finding's `Prompt for AI agent` and a runnable `verdict triage` command. Policy-withheld findings are never listed or copied. Flags: `--root`, `--check`, `--max-bundles` and `--json`. Bad frames, unknown methods and bad parameters return JSON-RPC errors naming the refusal code.
