---
"@stainless-code/verdict": minor
---

Add `fix --all`, the batch form of the bounded fix loop: it selects every open inline finding at or above `--min-severity` (default `important`) through the same selector `report` and `next` rank with, takes one pass per sig under the unchanged 3-passes-per-sig and one-file ceilings, and names each sig's outcome (`ready`, `ceiling`, `scope-escape`, `no-anchor`) in `data.batch`. `--paths <glob>` (repeatable) scopes a batch to matching anchor paths. A batch refuses `no-verify-cmd` unless the proving command is supplied by `--verify-cmd` or the bundle's `patch.json` `verifyCmd`. Every pass now also writes `fix.patch` beside the ledger — the worktree's unified diff against `HEAD`, never applied and never pushed — and refuses `fix-patch-secret` rather than write a credential shape into the bundle.
