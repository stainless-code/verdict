---
"@stainless-code/verdict": patch
---

`npx skills add stainless-code/verdict` now installs just the skill instead of
copying the whole repository — over a thousand files — because `SKILL.md` and its
payload moved into `skills/verdict/`. That directory holds exactly the files
`verdict skill install` writes.
