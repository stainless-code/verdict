---
"@stainless-code/verdict": patch
---

`examples/typescript-client/` now runs the mutating verbs end to end: `record` (including `--dry-run`), `post --dry-run`, and `config` validation, each against the locally built binary, a bundle the real `fetch-local` producer wrote, and a network-dead `gh` stub. The examples assert the envelope they read and print one checkpoint line per claim; the TS client gate (`make clients-ts`) now executes all three, so a stale example fails CI instead of rotting in the tree.
