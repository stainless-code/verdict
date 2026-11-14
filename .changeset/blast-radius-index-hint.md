---
"@stainless-code/verdict": patch
---

Make `index blast-radius` say which revision and checkout its missing index is
about, and how to answer for it — on both surfaces. The warning names the ref
the operator typed beside the tree it resolved to; the human line now ends
` — run <step>` (it used to name no command at all, because `next:` reaches only
`--json`), and `next:` carries the same step for JSON. The step is shell-quoted
(`--at 'feature/$USER'`, like `--from` already was), and a hand-off that named a
bare tree SHA gets words instead of a command — `verdict index build at a commit
whose tree is <tree>` — because `build` resolves a commit and refuses every tree
SHA with `bad-at`. The unindexed counts stay `null`, and refusal codes, exit
codes, and the envelope keep their shape.
