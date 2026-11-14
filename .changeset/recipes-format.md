---
"@stainless-code/verdict": minor
---

Declare review pipelines as data: `.verdict/recipes.json` is a strict, versioned
control document whose named entries state a lens set (with per-lens model hints
and dispatch caps), a synthesizer hint, and a budget, so a pipeline can be
diffed and shared instead of living in prompt prose. The format is published as
`schemas/recipes.schema.json` and the file joins the base-owned controls
inventory, so the document a PR is reviewed by is read at the PR base and a head
edit never steers it. The loader resolves a name — repository entry first, then
the embedded `default` — into the four fixed stages (`lenses`, `synthesize`,
`dedupe`, `rank`) and a canonical `sha256` over the selected entry; a bundle with
no recipe control resolves the built-in and refuses nothing. Two refusal codes
join the published set: `bad-recipe` (one diagnostic naming the JSON path and
the key) and `unknown-recipe` (the message lists the available names). Positions
are declarations, not selections: verdict never dispatches the stages and never
selects a model. The `run --recipe` surface that renders the plan lands on top
of this format.
