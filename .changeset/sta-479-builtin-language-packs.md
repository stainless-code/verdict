---
"@stainless-code/verdict": minor
---

The built-in layer now ships eleven curated language and ecosystem packs beside the core pack: `verdict/go`, `verdict/ts-js`, `verdict/python`, `verdict/rust`, `verdict/java`, `verdict/sql`, `verdict/terraform`, `verdict/github-workflows`, `verdict/dockerfile`, `verdict/shell`, and a cross-language `verdict/security` pack. Each carries one stack lens scoped to its file globs and three or four high-precision rules for real bug classes (lifecycle and resource leaks, injection, authz, migration and locking hazards, script injection, unpinned bases), and each ships an eval case, so `pack test <name>` grades it offline.

`lenses.txt` now carries only the lens bodies offered for the bundle's changed files, so a curated language lens reaches a review only when its stack is in the diff.

A repository can compose the built-in layer from its trusted-base `.verdict/config.json`: `packs.merge_builtin` turns the layer off, and `packs.disable_packs`, `packs.disable_lenses`, and `packs.disable_rules` name the built-in ids to drop. The read is anchored at the merge base with the default branch, so a branch commit cannot disable the built-in rules that review it, and a toggle that names nothing is a warning, never a refusal.

`verdict pack check <path> [--json]` resolves one repository-relative path through the same layers, merge, and globs a review uses: the packs in precedence order, the lenses offered for it, and the rules whose globs match it, each tagged with the winning pack and the ids it shadowed. It writes nothing and makes no host calls.
