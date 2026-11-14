---
"@stainless-code/verdict": patch
---

Every write to `.verdict/kb.jsonl` now serializes on the same OS advisory lock
the incident ledger uses: `learn --rule` and `learn --pack` append under it,
`learn --retire` and `learn --restore` rewrite under it above their read, and
the credit rewrite a `record`/`triage` flip performs holds it across its
read-modify-write. A second writer waits five seconds for the lock: `learn`
then refuses `store-busy` instead of dropping a row, and a flip drops the
staged credit with the usual retry warning instead of losing an append. The
persistent `.verdict/kb.jsonl.lock` joins the tracked `.verdict/.gitignore`
contract. `trend`, adopt's `--from auto` twin scan, and the serve listings now
stat a bundle home before listing it: a `.verdict/state` that is not a
directory, or will not list, refuses `corrupt-store` naming it instead of
reading as an empty store — an absent home still reads as empty, a bundle home
that exists but cannot list never reads as "no bundles", and serve keeps its
own `Stat`/`ReadDir` seams to reach the same decision.
