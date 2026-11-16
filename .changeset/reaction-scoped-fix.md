---
"@stainless-code/verdict": minor
---

Reaction-scoped fix rounds: a GitHub `fetch` now writes
`.verdict/state/<bundle>/selected.json` naming the finding sigs whose root
verdict review comment carries a human 👍, and the new `fix --selected` batch
addresses exactly those sigs.

The reaction read moves into one shared reader (`threadscore`) that `calibrate
--reactions` and `fetch` both use, so the pagination and the bot/self exclusions
are the same code: a bot's 👍 and a comment's own poster's 👍 never count, and a
reply is never a root, so only a comment verdict posted can select its sig.

`selected.json` is written only when something was reacted to — an empty
selection would claim a scope nobody asked for — a re-fetch after the reactions
go away deletes a stale one, and a failed reaction read keeps the last good
selection and warns instead of narrowing the work. GitLab and Forgejo write no
file, because verdict reads no reactions there.

`fix --selected` takes one bounded pass per selected sig with the same ceilings,
ledger, and `data.batch` outcomes as `--all`, prints the selection it read
(`data.selection` carries the path), and defaults its floor to `off` — the
reaction is the endorsement a severity floor would otherwise re-judge. It
refuses rather than printing an empty batch: `missing-selection` when no file
exists, `no-selected-findings` when the named sigs are no longer open inline,
and `corrupt-store` when the file is unreadable. It is also the one batch that
runs on a PR bundle, whose instructions land the fix through a re-fetch and
`post`.

Schemas: new `schemas/selected.schema.json` (docs/selected bundle document), and
the additive optional `data.selection` in the fix data profile.
