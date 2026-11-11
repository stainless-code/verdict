---
"@stainless-code/verdict": minor
---

Layer lens packs and list them. `verdict pack list [--json]` reports every
loaded pack (built-in, org from the personal settings `packs` array, repo from
`.verdict/packs/<name>/`) with its layer, source, canonical tree hash, lens
and rule ids, and drift (null until `verdict.lock`). Repo packs are read from
the trusted-base controls snapshot, so a PR cannot edit the pack that reviews
it; `post`/`record` re-hash the snapshot and refuse `controls-stale`. Duplicate
ids in one layer refuse `bad-pack`; a cross-layer override needs
`"replace": ["<lower pack name>"]` (downward only) and otherwise refuses
`pack-conflict`. A settings `packs` entry inside `.verdict/packs/` refuses
`bad-pack`, because that tree is the repo layer read at the PR base. Packs may
ship KB rules (`rules/<id>.md`, severity mapped through the manifest) that
join the fenced `kb.txt` context, never the trusted prompt. Manifest keys must
be exact lowercase, non-regular pack files and a symlinked pack root refuse
`pack-unsafe-path`, and body reads are bounded. A regular file directly under
`.verdict/packs/` is not a pack: `pack list` warns once and `fetch` ignores
it, while a symlink or special file there refuses `pack-unsafe-path` in both
reads. The read-only `pack` MCP tool, `Client.Pack`/`ListPacks`, and the
TypeScript `pack` method serve the same list.
