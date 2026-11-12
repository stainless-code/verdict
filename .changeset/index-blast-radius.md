---
"@stainless-code/verdict": minor
---

Add `index blast-radius --pr <n>|--paths <path>` — one change's reach. The
changed declarations come from the fetched bundle's `full.diff` (no host call)
or from named files; their callers are walked to depth 2, once per target and
cycle-safe; and candidate tests, co-change partners, and CODEOWNERS owners ride
along for the same paths. A diff section with no line hunks — a rename or a
mode change — reads as every declaration in its new path and warns that it did.
A `++` or `--` line inside a hunk is a content line, so its changed line and the
hunk's anchors stay put.
A depth-2 caller names the changed declaration it is reached from, not the depth-1 caller in between.
Every list publishes its count and truncation count, `co_changed` publishes its
distinct partner count beside the row count, and an absent index reads the
symbol, caller, and test counts as `null`, never 0. The read-only MCP `index`
tool serves the new query in both its `--paths` and `--pr` forms, and the
generated envelope schema types its output for the triage risk score.
