---
"@stainless-code/verdict": minor
---

**Breaking:** verify receipts now bind the content a command ran on, not HEAD plus a clean working tree.

- **What the content is.** It is the paths in your index, which is what a working-tree bundle's diff covers: tracked and intent-to-add (`git add -N`) files. Each one counts with its working-tree bytes exactly as on disk, so uncommitted edits are included. Untracked and ignored files are outside the content, as they are outside the bundle. Verdict's own state keeps its index version.
- **Where the command runs.** `verdict verify` copies that content into a snapshot, a private directory under the host temp root outside your checkout. It names exactly the bytes it copied as a git tree, then re-reads the snapshot to prove it still holds that tree. The command runs there, never in your checkout, so nothing done to your checkout during the run can change what it tested.
  - The snapshot is a clean, CI-like checkout. Ignored dependencies and local config are not in it, and submodules are empty, so install dependencies in the command itself. `verify` warns naming the untracked files, ignored entries and submodules it left out.
  - The receipt records the tree as `tree` in `verify-receipt.json` and `data.tree`.
  - The snapshot is removed afterwards, and a later run sweeps any snapshot a killed run stranded.
- **No checkout code runs.** Every git call runs with hooks off, even when `core.hooksPath` names `.husky/` or other tree content. Fsmonitor is off too, and no filter runs. Blobs are hashed with `--no-filters` and files are copied byte for byte, so no clean or smudge filter (git-lfs included), line-ending conversion or ident expansion runs or changes a byte. A checkout whose conversions change bytes names a different tree than its commit.
- **No in-place fallback.** A checkout `verify` cannot snapshot refuses `verify-failed` and nothing runs. Before, it fell back to running in your checkout.
- **Under `--sandbox`.** The snapshot is the writable root and your checkout is read-only. The snapshot's git directory lives in your checkout and is re-exposed read-only.
- **What `record` and `post` compare.** They compare the receipt's tree with the content they grade:
  - **Local bundle:** the tree `fetch-local` now records as `source.json` `content_tree`. A working-tree bundle names its content first and reads its diff, file list and stats from that tree (`git diff --no-ext-diff --no-textconv <base> <tree>`), so the bundle is exactly the content it records.
  - **`--staged` bundle:** records no tree, because `verify` never runs the index.
  - **PR bundle:** the head commit's tree.
- **The local fix loop.** It needs no commit: patch, re-run `fetch-local`, then `verdict verify`, then `record`.
- **Removed.**
  - `verify` outside a git checkout now refuses (`verify-failed`).
  - The clean-tree and HEAD binding is gone.
  - Receipts carry `tree` instead of `head`, and both receipts now have `schema_version` `"2"`.
  - `validation-<group>.json` replaces `execution_head` with `tree`.
  - `verify --group` requires the snapshot's content to equal the reviewed commit's tree.
  - A receipt that says `passed` but fails its own evidence reads as `deep validation contradictory`.
  - A `verification.json` claim is `{sig, verdict, verifyCmd}`. `resultHead` (the parsed-and-ignored `vrh`) is no longer read, and `verify_hint` drops it.
- **Triage.** A `triage[]` claim for `addressed` now refuses the review (`bad-review-input` from `record`, `bad-triage-status` from `post`) instead of being ignored with a warning.
