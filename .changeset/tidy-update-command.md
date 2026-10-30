---
"@stainless-code/verdict": patch
---

Fix the registry refresh command: `npx skills update` takes no package argument (bare form refreshes the detected scope, `-g` for global). The documented `update <package>` form never matches.
