---
"@stainless-code/verdict": patch
---

`verdict fix` no longer refuses PR bundles without `--selected`: a default pass, `--all`, `--recipe`, and `--test` now run on a bundle a `fetch` wrote, and the instructions and `next` hint name the landing that works there — push the fix, re-fetch, then land with `post`, since `record` refuses a PR bundle. On a PR bundle, `fix` refuses `head-mismatch` unless the checkout is on the head the bundle reviewed, so a pass, a patch, or `--test --commit` never lands on main or a stale branch. `fix` still writes only its ledger and patch beside the bundle and issues read-only git commands on either bundle kind. Corpus bundles still refuse with `not-local`, because a fix ledger does not belong inside a benchmark measurement input.
