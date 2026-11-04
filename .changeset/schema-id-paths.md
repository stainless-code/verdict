---
"@stainless-code/verdict": minor
---

**Breaking:** the published schemas moved with the skill, so every schema's
`$id` base path is now `skills/verdict/schemas/` rather than `schemas/` —
`https://github.com/stainless-code/verdict/blob/main/skills/verdict/schemas/envelope.schema.json`,
for example. If you resolve a schema by URL, or pin `$id` in a validator, update
the base path; the schema contents are unchanged.
