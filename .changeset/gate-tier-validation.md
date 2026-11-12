---
"@stainless-code/verdict": patch
---

An unknown gate tier now refuses `bad-flags` everywhere, not only on the CLI. The MCP `report` tool's `flags.check`
and the Go SDK's `Report.Check` and `Gate.MaxSeverity` used to pass any string through, so `check: "bogus"` gated on
every finding and could flip an informational-only bundle from passing to failing. The CLI keeps its existing
`--check` / `--max-severity` usage error.
