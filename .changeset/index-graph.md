---
"@stainless-code/verdict": minor
---

Extend `verdict index` with three graph queries. `index tests-for
<path>|--symbol <pkg.Name> [--at <tree>] [--limit <n>]` lists candidate tests
labelled `same-package`, `references`, or `imports-module` — tests worth
reading, never coverage claims; only `_test.go` files appear. `index co-changed
<path> [--commits <n>] [--since <dur|ref>] [--min-count <n>] [--limit <n>]`
reads one bounded `git log --no-merges` window (merges excluded, renames as two
paths), reports partner paths with co-change counts and four-decimal Jaccard
ratios, echoes the window it read, warns on a shallow clone, and caches the
window under `.verdict/index/cochange/` (two newest, timestamp-free bytes).
`index owners <path>` answers the CODEOWNERS rule at the tree — GitHub's file
order, last matching rule wins, the documented glob subset only — with
`owners: null` when nothing matches instead of an owner named nobody. The same
`target` object (path, owners, source) rides on `tests-for` and `co-changed`.
The read-only MCP `index` tool serves the three new queries and publishes their
query enum; the SDK forwards them.
