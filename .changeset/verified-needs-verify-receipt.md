---
"@stainless-code/verdict": minor
---

**Breaking:** a `VERIFIED` claim in `verification.json` no longer marks a finding `addressed` on its own. `record` and `post` also require the bundle's `verify-receipt.json`, which only `verdict verify` writes. A claim applies only when the receipt shows exit 0, no timeout, the claim's exact `verifyCmd`, the claim's finding when the receipt names one, and a tree equal to the graded content's (see the content-bound receipts entry). A hand-written claim or a pass written into `patch.json` is not enough: run `verdict verify` on the content you grade. Otherwise the finding keeps its status, the claim is logged as a `revalidation` event, and the run warns `VERIFIED claim for <sig> not applied (<reason>)`. `gate`, `post --approve`, MCP and `serve` all read the settled status. `NOT VERIFIED` still reopens a finding without a receipt.

A `triage[]` claim of `addressed` now refuses the review (`bad-review-input` from `record`, `bad-triage-status` from `post`) instead of being ignored with a warning.

`verdict verify` refuses a symlinked bundle directory, writes only through the directory it pinned before the run, and deletes the previous receipt before the command starts.
