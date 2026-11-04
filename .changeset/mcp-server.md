---
"@stainless-code/verdict": minor
---

Experimental MCP server. `verdict mcp` serves the review verbs to an MCP host
over stdio (newline-delimited JSON-RPC 2.0): no daemon, no HTTP, no new
dependency, sharing the host's credentials and exiting when stdin closes.
Tool schemas reflect the CLI request types, results are the `--json`
envelope's `data`, and typed refusals return `isError` with code, message, and
hint intact; `--read-only` exposes only the non-writing verbs, while `post` is
available in normal mode and in `--operator` mode. `verdict mcp config --agent
claude|cursor|codex` prints the exact host entry, and `--write` merges it
idempotently into the documented config (`.mcp.json`, `.cursor/mcp.json`, or
`$CODEX_HOME/config.toml`), backing the old file up to `<path>.verdict.bak`
and refusing unparseable or foreign entries instead of rewriting them. `--json`
prints one envelope, never prompts, and never starts the server; `verdict
--help` now lists the MCP entry so the surface is discoverable.
