---
"@stainless-code/verdict": patch
---

Make `index blast-radius` say which revision and checkout its missing index is
about, and how to answer for it. The warning names the ref the operator typed
beside the tree it resolved to; the human line ends ` — run verdict index build
--at <ref>` (or `--from <dir>` for a directory hand-off), and `next:` carries
the same command for `--json`, so neither output leaves the operator with a bare
`run verdict index build`. The unindexed counts stay `null`, and refusal codes,
exit codes, and the envelope keep their shape.
