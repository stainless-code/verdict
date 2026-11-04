---
"@stainless-code/verdict": minor
---

**Breaking (experimental MCP):** a host on `verdict mcp --read-only` no longer
sees a `threads` tool, and `report` there drops its `--output`/`--sarif`
arguments and reports `readOnlyHint: false` instead of claiming to write
nothing. Seven tools remain: `report`, `next`, `show`, `trend`, `ready`,
`doctor`, `check-tree`. A host that needs the thread feed should refresh with
`verdict threads` outside the server or run it without `--read-only`; a host
pinning `report` as a read-only tool should stop trusting that hint.
