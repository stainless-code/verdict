---
"@stainless-code/verdict": patch
---

`fetch` writes `.verdict/state/<bundle>/author.json`
(`schemas/author.schema.json`) from five harness signals in the PR title, body,
head ref, and commits: `{agent, model, confidence, evidence[]}`. It is advisory
data that never blocks or changes the review, and no signal writes no file —
absent means unknown, never human.
