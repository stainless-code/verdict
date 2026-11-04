---
"@stainless-code/verdict": patch
---

The docs site publishes its documentation guide as the `verdict-docs` skill
instead of a second skill named `verdict`. The `verdict` name belongs to the
review workflow — `skills/verdict/SKILL.md`, the payload `verdict skill install`,
`npx skills add stainless-code/verdict`, and the embedded binary ship — and a
harness that indexed both well-known skills loaded whichever won, handing an
agent that asked how Verdict works the review workflow, or the reverse. The
site's `/.well-known/agent-skills/index.json` now lists one skill,
`verdict-docs`, and the docs build fails if two published skills ever share a
`name:`.
