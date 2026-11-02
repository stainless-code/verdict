---
"@stainless-code/verdict": minor
---

MCP server: `post` is now a tool (writes a review and calls GitHub, so
`--read-only` omits it). New `verdict mcp config --agent <claude|cursor|codex>`
prints the exact host entry; `--write` merges it idempotently into the
documented config (`.mcp.json`, `.cursor/mcp.json`, or
`$CODEX_HOME/config.toml`), backs the old file up to
`<path>.verdict.bak`, and refuses unparseable or foreign entries typed instead
of rewriting them. `--json` prints one envelope; it never prompts and never
starts the server.
