---
"@stainless-code/verdict": patch
---

The index accuracy corpus's four scanner bugs are fixed, so the published
scorecard and the mechanism labels now match the manual. `index tests-for`
names a test whose own package imports the declaring package at
`transitive-import, depth 1` instead of dropping it, and `imports-module`
resolves a relative specifier (`import { tidy } from "../lib"`) against the
importing file's directory — including in a tree that also carries a `go.mod`,
where a scanned package's import path is now the directory rather than the Go
module path. `index callers` no longer attributes a Python attribute call
(`log.save(...)`) to a same-named module-level function, and a Go selector
written on a value outside the declaring package (`s.Save`, `a.Touch`) is
counted in `counts.unresolved` instead of disappearing, so an empty answer no
longer claims nothing was left unattributed.
