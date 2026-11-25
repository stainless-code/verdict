---
"@stainless-code/verdict": minor
---

**Breaking:** verify receipts now bind the content a command ran on, not HEAD plus a clean working tree.

- **What verify records.** Before the command runs, `verdict verify` names the checkout's content as a git tree id. That covers HEAD plus every modified, deleted, and untracked file `git add -A` sees, and leaves out verdict's own state. The id is computed through a throwaway index, so your index and refs are never touched. `verify` records it as `tree` in `verify-receipt.json` and in `data.tree`.
- **Where the command runs.** The command runs in a snapshot checkout of that tree, never in your checkout. Uncommitted edits are verified as they are, and nothing done to your checkout while the command runs can change what it tested. Under `--sandbox` the snapshot is the writable root. Your checkout is read-only, and its git directory is re-exposed read-only so git works in the snapshot even when the checkout sits under `/tmp`. When git cannot snapshot the content (no repository, or an untracked nested repository with no commit), the command runs in place with a warning, and its receipt names no tree, so it cannot back a `VERIFIED` claim.
- **What `record` and `post` compare.** They compare the receipt's tree with the content they grade. For a local bundle that is the tree `fetch-local` now records as `source.json` `content_tree`. When git cannot name that content, `fetch-local` still bundles, warns, and records no tree. For a PR bundle it is the head commit's tree.
- **The local fix loop.** The loop needs no commit: patch, re-run `fetch-local`, then `verdict verify`, then `record`.
- **Removed.** The clean-tree and HEAD binding is gone. Receipts carry `tree` instead of `head`. `validation-<group>.json` replaces `execution_head` with `tree`. `verify --group` now requires the checkout's content to equal the reviewed commit's tree, and it also runs in a snapshot. A `verification.json` claim is now `{sig, verdict, verifyCmd}`, and `resultHead` no longer gates anything.
- **Triage.** A `triage[]` claim for `addressed` now refuses the review (`bad-review-input` from `record`, `bad-triage-status` from `post`) instead of being ignored with a warning.
