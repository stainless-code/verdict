---
"@stainless-code/verdict": patch
---

Make two fetch refusals name their next step. An over-cap diff refuses
`diff-too-large` with a `next:` line naming the exclusions that already fit a
review — `--ignore <glob>` (repeatable on `fetch` and `fetch-local`), a
committed `.verdict/ignore` line, or paths after `--` on `fetch-local` — because
the cap is measured after them; the cap itself is unchanged. A tree the index
store never built refuses `missing-index` with the revision the operator typed
beside the tree it resolved to (`no index for HEAD (tree <tree>)`) and the exact
build command that answers for it (`next: verdict index build --at HEAD`, or
`--from <dir>` for a directory hand-off). Refusal codes, exit codes, and the
`--json` envelope keep their shape; only the message text is longer.
