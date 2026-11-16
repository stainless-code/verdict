---
"@stainless-code/verdict": minor
---

**Local code index**

- **`verdict index`** — build a local Go index with the standard library, stored by tree SHA so an empty commit reuses a tree and a rename parses nothing. [Code index](docs/manual.md#code-index) <!-- changesets: index-callers.md -->
- **Graph queries** — `index tests-for`, `index co-changed`, and `index owners` answer candidate tests, co-change partners, and the CODEOWNERS rule at a tree. [Code index](docs/manual.md#code-index) <!-- changesets: index-graph.md -->
- **Three more languages** — the index now reads TypeScript, JavaScript, and Python through two dependency-free lexical scanners, with no tree-sitter and no cgo. [Code index](docs/manual.md#code-index) <!-- changesets: index-scanners.md -->
- **`index blast-radius`** — one change's reach: callers to depth 2 plus candidate tests, co-change partners, and owners for the changed paths. [Code index](docs/manual.md#code-index) <!-- changesets: index-blast-radius.md -->
- **Transitive tests** — `tests-for` walks the reverse import graph up to `--depth` hops and labels tests it reached through imports. [Code index](docs/manual.md#code-index) <!-- changesets: index-transitive-tests.md -->
- **`fetch --index`** — hand an index you already built to `fetch` or `fetch-local` and the bundle gains an `impact` artifact and a fenced `## impact` section. [Impact hand-off](docs/manual.md#impact-hand-off-fetch---index) <!-- changesets: fetch-index-impact.md -->
- **Missing-index hints** — `index blast-radius` and `fetch` now name the revision and tree an absent index is about, with the exact shell-quoted build step that answers for it. [Code index](docs/manual.md#code-index) <!-- changesets: blast-radius-index-hint.md -->
- **Scanner fixes** — four corpus bugs are fixed: a transitive test no longer disappears, relative imports resolve, a Python attribute call is no longer misattributed, and a Go selector outside its package is counted unresolved. [Code index](docs/manual.md#code-index) <!-- changesets: index-corpus-bugs.md -->
- **One impact budget** — the impact section's byte budget is shared across symbols, callers, and tests, each list carrying its own omitted count. [Code index](docs/manual.md#code-index) <!-- changesets: index-impact-budget.md -->
