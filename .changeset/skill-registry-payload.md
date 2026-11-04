---
"@stainless-code/verdict": patch
---

`npx skills add stainless-code/verdict` now installs only the skill. It used to
copy the whole repository (over a thousand files, 8 MB) because `SKILL.md` sat
at the repository root. The skill now lives alone in `skills/verdict/`
(`SKILL.md`, `prompts/`, `schemas/`, `scripts/verdict`), the same files
`verdict skill install` writes. The published schemas moved with it, so each
schema's `$id` URL now points at `skills/verdict/schemas/`; their contents are
unchanged.
