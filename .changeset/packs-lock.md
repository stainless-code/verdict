---
"@stainless-code/verdict": minor
---

Add `verdict pack add <dir|git-url>[@ref]`, which validates a lens pack with
the existing loader, vendors its tree under `.verdict/packs/<slug>/`
(including the `evals/` and `examples/` payloads), and pins it in
`.verdict/verdict.lock`. The lock's tree hash covers those payload trees too,
so two packs that differ only in an eval case record different hashes. The lock is strict JSON with one row
per repo pack, sorted by name: `name`, `source` (`dir:<path>` or
`git:<url>@<sha>`), `version`, the canonical tree `hash`, and an integrity
`sig` over those fields. A `dir:` source is a slash path with no control
character, backslash, or leading `-`; neither spelling carries a userinfo
password. Re-adding a name replaces its tree and entry and
prints the old and new hash; a source inside `.verdict/packs/` refuses
`bad-pack`. Git sources fetch hermetically (no system config, scrubbed
`HOME`/`XDG_*`, no prompts, no hooks, file protocol disabled, shallow clone
without tags or submodules) and record the resolved HEAD commit; the git calls
share a five-minute budget and the clone is checked against the pack caps
before it loads. A URL
userinfo password or token refuses `bad-pack` before the clone and never
reaches a printed line, a refusal message, or `verdict.lock`; the bare
`user@host` forms keep working, and a hand-written lock row carrying one
refuses too.

`fetch`, `fetch-local`, `post`, and `record` now refuse `pack-lock-missing`
when a repo pack has no lock entry and `pack-lock-drift` when the tree hash,
pack directory, or `sig` disagrees, from one shared check with no bypass
flag. `.verdict/verdict.lock` joins the controls inventory, so it is read at
the PR base and an edited snapshot copy refuses `controls-stale`. `pack list`
fills its `drift` column with null for a matching repo pack and the refusal
code otherwise; the human rows show it, and a lock row whose pack directory
is gone lists with `drift: "pack-lock-drift"`. The built-in pack stays pinned
by the binary and org packs stay in per-operator settings, so neither is
locked.
