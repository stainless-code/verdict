---
"@stainless-code/verdict": minor
---

`fix --test <sig>` promotes one reproduced finding instead of passing it again: it copies the bundle's retained reproduction artifact into the reviewed worktree at `.verdict/tests/<sig>-<hash>.<ext>`, rewrites the receipt command to that path, consumes no pass, and writes no ledger. `--commit` stages that one file and commits it as `test: regression for <sig>` through a pathspec commit, so work already staged in the index stays staged and out of the commit; it needs `--test`. A target already holding different bytes refuses `test-conflict`, a finding with no retained artifact refuses `no-reproduction`, and a `.verdict` or `.verdict/tests` that is a symlink refuses rather than land the test outside the checkout. `tests.draft` in `.verdict/config.json` now decides whether `record` retains the draft at all: `on-reproduce` (the default) keeps it, `off` refuses the capture.
