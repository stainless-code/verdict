---
"@stainless-code/verdict": minor
---

`verdict ingest <owner/repo> <pr> --sarif <file|->` ingests SARIF 2.1.0 results
into the bundle's `findings.json` store with `source: "tool"`, sharing one
store, one dedupe path (`verdict/sig`, `sig`, and `(tool, fingerprint)`), and one
suppression/withheld path with model reviews. Severities map through
`skills/verdict/schemas/sarif-map.json`, out-of-diff results stay
`anchored: false`, excluded paths land withheld (`source: "exclusion"`), and tool
findings keep `rigor: "said"` (`rigor_source: "default"`).

`report` and `trend` publish `by_source` (`model` and `tool`) breakdowns,
`ingest` joins the CLI, MCP default tool registry (`mutates: localWrites`), Go
SDK (`pkg/verdict`), TypeScript client (`clients/ts`), and published JSON
Schemas.
