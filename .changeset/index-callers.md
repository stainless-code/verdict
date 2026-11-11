---
"@stainless-code/verdict": minor
---

Add `verdict index`: a local Go code index built with the standard library
(`go/parser`, `go/ast`) and stored under `.verdict/index/` by tree SHA.
`index build [--from <dir>] [--at <ref>]` writes one tree file plus
content-addressed shards, so an empty commit reuses the tree, a pure rename
parses nothing, and an edit parses one blob. `index status` reports the stored
trees and shards, `index prune` keeps the newest four trees and the shards they
reference, and `index callers <symbol> [--at <tree>] [--limit <n>]` returns
call sites labelled `exact` within a package or `lexical` across an import
edge; method values, dynamic dispatch, reflection, and generic instantiation
are counted as unresolved, never guessed. A package qualifier that matches no
indexed package refuses `bad-flags` rather than reading as no callers. Caps
refuse `index-capped` with the measured number, a truncated shard refuses
`index-corrupt`, and a query against a tree that is not HEAD's warns and sets
`stale: true`. The MCP `index` tool is read-only and serves `callers` and
`status`; the SDK forwards every subcommand. `.verdict/index/` joins the
tracked ignore contract.
