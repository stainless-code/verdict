"@stainless-code/verdict": patch
---

Published JSON Schemas for the review input, findings store, cost record, and the `--json` envelope (generic shape plus stable per-verb `data` for report, post, and record), shipped in the npm tarball. An offline `schema-contract` eval validates real fixture outputs against them, and the manual states the drift rule: additive within schema version 1, new version only by operator decision.
