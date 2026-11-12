---
"@stainless-code/verdict": minor
---

Ship the index hand-off. `fetch` and `fetch-local` take
`--index <ref|dir>`: the operator names an index they already built — a
revision in the reviewed repository, or a directory holding `.verdict/index` —
and the bundle gains an `impact` artifact and a `## impact` section on
`toc.txt`, inside the existing nonce fence. Nothing is built, refreshed, or
discovered: without the flag no section is written and an earlier run's
`impact.json` is removed.

`impact.json` (named in `data.artifacts`) holds the answered tree and commit,
`stale`, `coverage`, the diff's changed `paths`, the row `limit`, and one
measured object per list — `symbols`, `callers` (with `unresolved`), and
`tests` (with `depth_truncated`) — each carrying `count`, `truncated`, and
`rows`; a list the index could not answer reads `null`, never `0`. `sources`
carries `index:<tree>`, naming the tree that answered. The `toc.txt` section is
capped at 8 KiB and ends with `truncated: <n> line(s) omitted to fit the 8 KiB
impact cap` when rows do not fit, and every path and symbol it renders is
escaped through the shared fence rule, so a hostile name can neither close the
fence nor start a line of its own.

A directory with no index store refuses `bad-flags`, an unresolvable ref
`bad-at`, a tree the store never built `missing-index`, and a truncated tree
file `index-corrupt`; a named index answering for another tree, and a bundle
head this clone cannot see, warn visibly and proceed. The four hashed prompt
files are untouched, so `prompts_hash` does not move.
