---
"@stainless-code/verdict": minor
---

Experimental `verdict mcp`: the review verbs as MCP tools over stdio
(JSON-RPC 2.0, newline-delimited). No daemon, no HTTP, no new dependency —
the process shares the host's credentials and exits when stdin closes. Tools
carry schemas reflected from the core request structs, success results are the
CLI `--json` envelope's `data`, and typed refusals return `isError` with
code/message/hint intact; `--read-only` exposes only non-writing verbs.
