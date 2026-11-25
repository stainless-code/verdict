---
"@stainless-code/verdict": minor
---

`verdict serve --allow-post` adds confirmed host posting. The Detail view's `Submit Review` button (PR bundles only) previews the post, shows every comment it will send and the preview's warnings, and posts only from the confirmation dialog. Without `--allow-post` the route refuses `403 post-disabled`.

`POST /v1/post/submit` spends a single-use preview nonce, re-renders the review under the bundle lock, and refuses `409 stale-preview` unless it matches the preview. The post is pinned to a digest of the exact payloads the host receives, so a later change refuses `409 post-content-changed` before any host write. Every guard of the CLI `post` holds, and a served post authenticates and renders exactly as the CLI's does. A preview over 256 KiB of text mints no nonce, so Confirm stays disabled; check it with `verdict post <repo> <pr> --dry-run` and post from the CLI. The submit is bounded to five minutes (`post-timeout`), and Ctrl-C stops an in-flight host call. The `--json` start envelope reports `read_only` and `allow_post`.
