---
"@stainless-code/verdict": minor
---

**Lens packs**

- **Built-in lenses as data** — `perf`, `api-contract`, and `test` now ship as a pack resolved into every bundle as `lenses.txt`, with the four hashed prompt files byte-identical. [Lens packs](docs/manual.md#lens-packs) <!-- changesets: lens-packs-s1.md -->
- **No per-lens cap** — a lens returns every finding its evidence supports: the built-in pack declares only `max_files: 12`, and the dispatch template leads with the lens's domain. [Lens packs](docs/manual.md#lens-packs) <!-- changesets: lens-dispatch-deltas.md -->
- **`pack add` and a lock** — vendor a pack from a directory or git source, validated before it loads and pinned in a strict `.verdict/verdict.lock`. [Lens packs](docs/manual.md#lens-packs) <!-- changesets: packs-lock.md -->
- **Layered packs** — `pack list` reports every loaded pack with its layer, source, tree hash, ids, and drift, and a repo pack is read from the trusted base so a PR cannot edit the pack that reviews it. [Lens packs](docs/manual.md#lens-packs) <!-- changesets: pack-layers.md -->
- **`pack test`** — run a pack's eval cases offline. [Lens packs](docs/manual.md#lens-packs) <!-- changesets: pack-test.md -->
- **Leftover backup sibling** — a failed sweep of a previous tree is reported as success plus a warning naming the leftover, instead of refusing an add whose new tree is already durable. [Lens packs](docs/manual.md#lens-packs) <!-- changesets: pack-add-backup-warning.md -->
- **Stale staging** — a crashed re-add's leftover staging or backup sibling refuses `pack-staging-stale` with the path to delete. [Lens packs](docs/manual.md#lens-packs) <!-- changesets: pack-staging-stale.md -->
