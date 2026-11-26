---
"@stainless-code/verdict": minor
---

**Breaking:** `verdict ingest --sarif <file|->`, the older spelling of `--from sarif <file|->`, is removed and now fails as an unknown flag. The MCP `ingest` tool and the Go SDK's `IngestRequest.From` are now required; both used to treat an empty value as `sarif`, and an empty one now refuses `missing-sarif`. `bench/runner` drops `-runs`, the older spelling of `-repeats`. Migration: write `ingest --from sarif <file|->`, pass `from: "sarif"` to MCP `ingest` and `From: verdict.SourceSARIF` to the SDK's `Ingest`, and use `-repeats` with the bench runner.
