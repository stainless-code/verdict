---
"@stainless-code/verdict": minor
---

Give a review the revert memory that belongs to its diff. When a changed path
matches a row of the tracked `.verdict/reverts.jsonl` ledger (`verdict backtest
reverts`), `fetch` and `fetch-local` write
`.verdict/state/<bundle>/reverts.txt`: one `<<<UNTRUSTED …>>>` block per matching
revert inside `internal/untrusted`'s fence, with the machine facts and the
author's paths first and the author's subject last, so a cap can only ever cut
subject text. A match is the same path, or a row path that is a directory
prefix of it; rows are read through one validating reader
(`internal/reverts`) and sorted by `sha`, so the file is a function of the
ledger and not of its line order.

The note is bounded at 20 reverts or 64 kB, and the truncation line names the
cap it hit and points at `.verdict/reverts.md` for the rest. It joins the
bundle contract: `data.artifacts["reverts.txt"]`, the `--show-prompts` dump, and
`provenance.json`'s `bundle` inputs all carry it, so the receipt records its
bytes without copying an author's text.

Memory is context, never orders. `prompts/review.md` gains one clause beside
`kb.txt` telling the reviewer to read the file when `fetch` reports it and to
treat a revert as evidence the change broke something once — the prompt-set hash
moves once, to
`71e81dd0a06320e6252cd6ddc49e0d9591a61684a254568a653a7b338bcb1857`, and the
record and fetch goldens follow it.

Nothing is a gate. No intersection removes the file, an absent ledger is
silence, and a ledger this build cannot read warns once naming the store and the
field defect and writes nothing: `fetch` only reads the ledger, so a scan
holding its lock never blocks a fetch, and no revert byte reaches `review.json`
or a post.
