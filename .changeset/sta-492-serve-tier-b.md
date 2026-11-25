---
"@stainless-code/verdict": minor
---

`verdict serve` adds operator jobs: `POST /v1/jobs/verify` runs a sandboxed `verify --sandbox` over a bundle, and `POST /v1/jobs/post-preview` runs a dry-run `post`, with `GET /v1/jobs/{id}` and `POST /v1/jobs/{id}/cancel`. Jobs hold a per-bundle lock, run with bounded concurrency, and stream their state on `/v1/events`. The Detail view gains `Reproduce in sandbox` and `Preview PR post` buttons when the server allows local writes.
