---
"@stainless-code/verdict": minor
---

**Breaking:** `verdict ingest --sarif <file|->`, the older spelling of `--from sarif <file|->`, is removed and now fails as an unknown flag. The MCP `ingest` tool requires `from`, which it used to treat as `sarif` when omitted. `bench/runner` drops `-runs`, the older spelling of `-repeats`. Migration: write `ingest --from sarif <file|->`, pass `from: "sarif"` to MCP `ingest`, and use `-repeats` with the bench runner.
