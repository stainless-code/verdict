---
"@stainless-code/verdict": patch
---

`verdict mcp` now joins its protocol read loop on shutdown when given a closable reader. A goroutine blocked on a non-closable source can outlive `Serve`. `verdict serve` now ends open event streams when the HTTP listener fails, not only on a clean shutdown. The CLI now reads its environment only through the injected source, so host variables no longer leak into a run that was given its own.

`report --validation` without `--json` now prints a validation table: the mode, the exact-file hash, and the counts, then one row per judged finding with its verdict, severity move, and reason. `post` and `record` now warn when `validation.json` records a `validator.prompt_sha256` that differs from the `prompts/validate.md` this build ships, and still apply the pass.

`drain --agent --post` refreshes threads after drafting so resolved or changed threads are skipped. Drain receipts retain outstanding failures across runs and clear recovered feed-save failures. Validation reports omit rows when the current artifact differs from the recorded pass, and prompt drift warnings escape control characters.
