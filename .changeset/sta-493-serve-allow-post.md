---
"@stainless-code/verdict": minor
---

`verdict serve` adds Tier C confirmation-gated host posting behind an
explicit `--allow-post` (requiring `--interactive`, never implied by it
alone): `POST /v1/post/submit` accepts `{root, bundle, preview_digest,
preview_nonce, confirm}`, spends a fresh single-use preview nonce with
`confirm: true`, re-checks the bundle fingerprint under its store lock,
and delegates directly to `core/post` with `DryRun: false` so every
stale-SHA, duplicate-post, anchor, own-PR, and binding-approval guard and
the CLI `post` receipts hold. With `--interactive` alone the route refuses
`403 post-disabled` before any host client; without `--interactive` it is
the read-only `405`. The Detail view renders `Submit Review` only when
`/v1/health` advertises `allow_post: true`, previews through the Tier B
job, and posts only from the confirmation modal.
