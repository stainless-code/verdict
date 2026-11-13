---
"@stainless-code/verdict": minor
---

Declare every MCP tool's local mutation class explicitly and derive read-only
exposure from it. Calls hidden by `--read-only` now return the typed
`read-only-tool` tool refusal with a remediation hint.
