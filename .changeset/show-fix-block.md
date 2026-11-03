---
"@stainless-code/verdict": patch
---

`show --json` gains an additive `data.fix` block beside the unchanged `data.item`: full `sig`, `status`, `severity`, deduped `files`, `locator`, `suggestion`, a `verify_hint` (`{verifyCmd, resultHead}`) read from the bundle's `verification.json`/`patch.json`, and a fixed untrusted-evidence `caution`. The block is pure read — no flags, no execution, no store writes — and the text path is unchanged. Missing history reads as null, never invented text.
