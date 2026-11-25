---
"@stainless-code/verdict": minor
---

`fix --test <sig>` promotes a reproduced finding's test into your checkout: it copies the bundle's retained reproduction to `.verdict/tests/<sig>-<hash>.<ext>` and points the receipt command at it. `--commit` (with `--test`) commits only that file as `test: regression for <sig>`, leaving other staged work staged. A target holding different bytes refuses `test-conflict`, a finding with no retained reproduction refuses `no-reproduction`, and a symlinked `.verdict` or `.verdict/tests` refuses. `tests.draft` in `.verdict/config.json` decides whether `record` keeps the reproduction: `on-reproduce` (the default) or `off`.
