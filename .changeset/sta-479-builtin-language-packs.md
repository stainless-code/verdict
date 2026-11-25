---
"@stainless-code/verdict": minor
---

The built-in layer now ships eleven curated language and ecosystem packs beside the core pack: `verdict/go`, `verdict/ts-js`, `verdict/python`, `verdict/rust`, `verdict/java`, `verdict/sql`, `verdict/terraform`, `verdict/github-workflows`, `verdict/dockerfile`, `verdict/shell`, and a cross-language `verdict/security`. Each carries a stack lens scoped to its file globs and a few high-precision rules for real bug classes, plus an eval case that `pack test <name>` grades offline. `lenses.txt` now carries only the lenses for the bundle's changed files.

A repository can tune the built-in layer from its trusted-base `.verdict/config.json`: `packs.merge_builtin` turns it off, and `packs.disable_packs`, `packs.disable_lenses` and `packs.disable_rules` drop built-in ids. The setting is read at the merge base with the default branch, so a branch cannot disable the rules that review it.

`verdict pack check <path> [--json]` shows how one path resolves: the packs in precedence order, the lenses offered, and the matching rules with the pack that won each. It writes nothing and makes no host calls.
