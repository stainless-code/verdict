---
"@stainless-code/verdict": minor
---

The built-in language and ecosystem packs were upgraded to version-aware,
fact-checked rules and a new `verdict/react` pack. Every shipped rule now
carries a concrete `## When to flag` section (including the version or runtime
precondition that decides the answer) and a `## Do NOT flag` section that
names the safe idioms, linter duplicates, and modern defaults the rule must
not report.

New and deepened coverage: Go loop-variable capture gated on a `go.mod` below
Go 1.22, Go 1.23 timer collection, context cancel leaks, `rows.Err`, `%w`,
slice aliasing, deferred cleanup in loops, and mutex misuse; general TS/JS
floating promises, prototype pollution, boundary validation, numeric sort,
regex injection, and long-lived handler cleanup; Python bare `except` and
cancellation, late-binding closures, SQL formatting, and async blocking; Rust
blocking-in-async, dropped join handles, and narrowing casts; Java interrupt
handling, `Optional.get`, and JDBC concatenation; SQL unbounded writes and
reads; Terraform public admin ingress and unencrypted storage; GitHub Actions
permissions; Dockerfile remote install scripts; shell `eval` and predictable
`/tmp`; and four new security rules (timing-safe comparison, CSPRNG, JWT
validation, path traversal/zip-slip).

`verdict/react` is a twelfth curated pack, scoped to `**/*.tsx` and
`**/*.jsx` so backend and CLI TypeScript repositories never receive React
rules. It covers effect cleanup, stale closures, index keys on dynamic lists,
derived state, conditional hooks, raw HTML, and the server/client boundary;
the old `ts-js-effect-cleanup` rule moved here.

Every curated pack now ships a positive eval case and a negative-trap safe
fixture. `pack test <name>` validates rule selection and finding citations
offline and pins the safe fixture; it runs no model, so a judgement-level
false positive remains a measurement question. `internal/packtest` asserts
each pack has the pair, that the negative-trap diff selects at least one of
the pack's rules, and that every rule body carries the When/Do-NOT/Evidence
sections.
