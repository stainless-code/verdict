---
"@stainless-code/verdict": patch
---

`learn --rule` and `learn --pack` now serialize their append to
`.verdict/kb.jsonl` with the same OS advisory lock the incident ledger uses:
a second writer waits five seconds and then refuses `store-busy` instead of
interleaving two read-modify-writes and dropping a row, and the persistent
`.verdict/kb.jsonl.lock` joins the tracked `.verdict/.gitignore` contract.
`trend`, adopt's `--from auto` twin scan, and the serve listings now stat a
bundle home before listing it: a `.verdict/state` that is not a directory, or
will not list, refuses `corrupt-store` naming it instead of reading as an
empty store — an absent home still reads as empty, and a bundle home that
exists but cannot list never reads as "no bundles".
