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
`/v1/health` advertises `allow_post: true` and the bundle is a PR number
(`local-*` has no pull request to post to), previews through the Tier B
job, and posts only from the confirmation modal. The preview and the
confirmed post carry the server's environment and resolved render
configuration, so a served post authenticates and renders exactly like the
CLI's; the `--json` start envelope reports `read_only` and `allow_post`.
The confirmation modal polls its preview job with a bounded 250 ms backoff
instead of an unpaced burst.
The confirmed submit is bounded to five minutes: a stalled host call is
stopped, the bundle lock is released, and the route refuses `post-timeout`
instead of blocking every triage and job on the bundle. The served post
also resolves the tool version from the install root exactly as `verdict
post` does, so a `package.json` in the server's working directory can no
longer stamp its version into a posted review.
