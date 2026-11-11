---
"@stainless-code/verdict": minor
---

Ship the built-in lenses as a pack. The three lenses `prompts/review.md` step 4
names today — `perf`, `api-contract`, and `test` — now live as data in
`skills/verdict/packs/builtin/`: a strict JSON `pack.json` (published as
`schemas/pack.schema.json`) plus one markdown body per lens. `fetch` and
`fetch-local` resolve the embedded pack into every bundle as `lenses.txt`
(each body delimited and labelled with its id and eight-hex hash), list it in
`data.artifacts`, and append `lens_pack=<name>@<version>` and
`lenses=[<id>:<hash8>,…]` to each `plan.txt` group line. A lens whose globs
match none of a group's files is not offered for that group. The loader is
capped (64 KiB/file, 1 MiB/pack, 256 files, 64 lenses), refuses `bad-pack`,
`pack-too-large`, and `pack-unsafe-path` (absolute paths, `..`, symlinks), and
hashes the canonical tree. The four hashed prompt files are byte-identical, so
`prompts_hash` does not move.
