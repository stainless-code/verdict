---
"@stainless-code/verdict": minor
---

`verdict acp` starts a lightweight editor-agnostic ACP v1 stdio server: newline-delimited JSON-RPC 2.0 on stdin/stdout, backed exclusively by `internal/core/query` with zero model calls, zero writes, and zero daemon. `initialize` negotiates `protocolVersion: 1` and advertises read-only capabilities under the `verdict` identity; four `verdict/`-namespaced extension methods carry the reads — `verdict/bundles/list` lists bundles, `verdict/findings/list` lists findings with evidence rungs, `verdict/findings/get` reads one finding, and `verdict/findings/copy` returns the deterministic `Prompt for AI agent` plus the runnable `verdict triage` command, with policy-withheld findings never listed and never copied. Flags are `--root`, `--check`, `--max-bundles`, and `--json`; malformed frames, unknown methods, invalid params, missing bundles, and traversal names refuse as clean JSON-RPC errors naming the refusal code.
