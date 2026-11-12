---
"@stainless-code/verdict": minor
---

Reach candidate tests through the import closure. `index tests-for
<path>|--symbol <pkg.Name> [--at <tree>] [--depth <n>] [--limit <n>]` walks the
reverse import graph from the declaring package — breadth first, cycle safe,
`--depth` hops (default 3, maximum 8, 1 turns the walk off) — and labels a test
whose own package reaches it through imports of its own `transitive-import`,
alongside `same-package`, `references`, and `imports-module`. Each row carries
the import hops its own imports took — never its directory's shortest path, so a
sibling test that imports the target directly cannot hide it — and a direct
candidate at depth 1 never reads as a transitive one. `depth_truncated` counts
the candidates whose own chain is one hop past the cap. The walk uses each
language's own import data (Go import paths, TypeScript
and JavaScript specifiers, Python modules and the submodule a `from … import …`
names) and never crosses a language family. `index blast-radius` reuses it, so
its candidate-test list gains transitive rows; the read-only MCP `index` tool
takes `depth` too.
