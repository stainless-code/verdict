---
"@stainless-code/verdict": patch
---

Make `index blast-radius` say which revision and checkout its missing index is
about. The warning names the ref the operator typed beside the tree it resolved
to, and the `next:` line is the exact build command that answers for it
(`verdict index build --at <ref>`, or `--from <dir>` for a directory hand-off)
instead of a bare `run verdict index build`. The unindexed counts stay `null`,
and refusal codes, exit codes, and the `--json` envelope keep their shape.
