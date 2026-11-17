---
"@stainless-code/verdict": minor
---

`drain --post --dry-run` runs every post gate and live read, then writes `drain.dryrun.json` with the exact marked replies and typed skips a real post would produce. It makes zero host writes and leaves `replies.json`, `drain.json`, and `threads.json` byte-identical; `--agent --post --dry-run` plans, drafts, then previews. The flag requires `--post` and secret-shaped bodies still refuse.
