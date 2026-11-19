---
"@stainless-code/verdict": minor
---

Verdict now writes an append-only per-bundle invocation journal (`<bundle>/journal.jsonl`) for every bundle-writing CLI and MCP call, and adds the read-only `verdict journal <owner/repo> <pr|local-name> [--json]` verb (plus its MCP tool, Go facade method, TypeScript client method, shell completion, and `schemas/journal.schema.json`). Each call records paired `start` and `end` lines with redacted `argv`, content-digest `bundle.before`/`bundle.after` tamper-evidence hashes, exact `wrote`/`removed` paths with SHA-256 digests, exit code, refusal code, warnings count, and `next` hint, bounded at 1 MiB with atomic whole-line truncation and non-fatal append warnings. `provenance.json` and `run.json` exclude `journal.jsonl` and its lock so receipts stay byte-deterministic.
