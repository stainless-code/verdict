---
"@stainless-code/verdict": patch
---

`verdict mcp` shuts down cleanly even when a read is blocked on its input. `verdict serve` ends open event streams when its listener fails. The CLI reads its environment only through the source it was given, so host variables no longer leak into a run that was given its own.

`report --validation` without `--json` now prints a validation table: the mode, the file hash, the counts, and one row per judged finding with its verdict, severity move and reason. With `--json` it uses the saved tally when a receipt exists. `post` and `record` warn when `validation.json` was produced by a different `prompts/validate.md`, and still apply it.

`drain` serializes writes to the same bundle, and `drain --agent --post` refreshes threads after drafting so resolved or changed threads are skipped. Replanning keeps earlier posting receipts, outstanding failures persist across runs, and a retry clears a failure whose thread was resolved or whose draft is obsolete.
