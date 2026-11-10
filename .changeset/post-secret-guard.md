---
"@stainless-code/verdict": patch
---

Refuse to publish secret-shaped values. `post` scans the summary body and each
finding's body and suggestion before the first host call, and a credential
value — a PEM private key, an AWS key id, a GitHub, Slack, Google, Stripe,
Anthropic, or OpenAI token — refuses `post-secret-shaped`, naming the site and
the matched shape but never the value: a posted review cannot be unsent.
`--dry-run` warns in the same words and still exits 0. `--allow-secret-values`
publishes the review anyway, and `post.json` records the shapes it let through
in `secret_shapes_allowed[]`. The patterns move to `internal/envsecret`, so
`fetch`'s sensitive-content scan and this guard share one table; fetch's
bundles and goldens are byte-identical, and so is the receipt of a review that
carries no such value.
