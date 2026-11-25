---
"@stainless-code/verdict": minor
---

The built-in packs are now version-aware and fact-checked, and a new `verdict/react` pack joins them. Every rule has a `When to flag` section, including the version or runtime condition that decides it, and a `Do NOT flag` section naming safe idioms and linter duplicates.

Coverage grows for Go (loop-variable capture before Go 1.22, timers, context leaks, `rows.Err`, `%w`, slice aliasing, mutexes), TS/JS, Python, Rust, Java, SQL, Terraform, GitHub Actions permissions, Dockerfiles and shell, plus four security rules: timing-safe comparison, CSPRNG, JWT validation and path traversal. `verdict/react` applies only to `*.tsx` and `*.jsx` and covers effect cleanup, stale closures, index keys, derived state, conditional hooks, raw HTML and the server/client boundary. Every curated pack ships a positive eval case and a safe negative fixture, which `pack test <name>` checks offline.
