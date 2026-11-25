---
"@stainless-code/verdict": minor
---

`verdict ingest --from sarif <file|->` imports a SARIF 2.1.0 document from any analyzer (Semgrep, CodeQL, golangci-lint, Ruff, Trivy, or `verdict report --sarif`) under the 8 MiB cap; `--sarif <file|->` still works. One source per call: mixing or repeating the flags is a usage error, and any other `--from` format refuses `bad-from`.

Each result is graded against the bundle's diff, not the scanner's word. The diff line becomes the finding's `quote`, an anchored result grades `rigor: "pointed"`, and a snippet that disagrees with the line is skipped. A result with no location, a bad line, a path outside the repository or the diff, or an ambiguous path is listed in `data.skipped[]` with its reason, never dropped silently. Absolute, drive-letter, traversal and host-bearing `file://` URIs are refused. Results dedupe by finding signature and by tool fingerprint, and `report --sarif` now keeps `tool`, `rule_id` and `fingerprint` under `properties`, so its export re-ingests as the same findings.
