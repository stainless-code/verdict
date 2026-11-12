---
"@stainless-code/verdict": minor
---

Scan TypeScript, JavaScript, and Python in `verdict index`. A build now reads
`.ts`, `.tsx`, `.mts`, `.cts`, `.js`, `.jsx`, `.mjs`, `.cjs`, `.py`, and
`.pyi` alongside Go, so `index callers`, `index tests-for`, and `index status`
answer for those files too. Go stays parsed with `go/parser`; the other three
are read by two dependency-free lexical scanners — no tree-sitter, no cgo, no
third-party module — that tokenize the file and record declarations, imports,
and call sites. Every scanned file publishes its `language`, a build publishes
`data.languages` with one row per scanned language and how many of the indexed
files it holds, and a
caller row names the language it came from. A scanned language resolves one way
only: `exact` never appears, and a name is attributed as `lexical` inside the
directory that declares it, so a caller list for a scanned symbol is a floor
rather than a proof. Languages never cross: a caller row, or a same-directory
test candidate, must share the declaration's language family, so Go, Python, and
the JavaScript family each answer only for themselves. Test candidates follow the language's own naming — `.test.`
and `.spec.` names and `__tests__` directories for the JavaScript family,
`test_*.py`, `*_test.py`, `conftest.py`, and `tests`/`test` directories for
Python — and a test in another directory is a candidate only when it imports
the queried file by a specifier that resolves to it. A blob two languages both
claim is parsed per file and cached under neither, so a shared file never
borrows another language's parse. Files over the 1 MiB cap or in an extension
the index does not scan stay out of the tree and count as skipped. A store
written by an earlier build (store version 2 now) reads as unbuilt in `index
status` and is rebuilt once by the next `index build`.
