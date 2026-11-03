"@stainless-code/verdict": patch
---

Supported CI automation recipe with merge gating: a new `docs/ci-recipe.md`
page and a copy-paste `examples/verdict-review.yml` workflow run fetch →
agent review → post → the `report --check`/`ready` merge gate in the
operator's own CI with their own runner and key, using the setup-verdict
action. Every action pins a full commit SHA, and a new eval case grades the
page, the pins, and the gate mechanics so the recipe cannot rot.
