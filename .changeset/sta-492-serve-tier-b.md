---
"@stainless-code/verdict": minor
---

`verdict serve --interactive` adds Tier B operator jobs over the existing SSE
channel: `POST /v1/jobs/verify` queues a strictly sandboxed `verify` and
`POST /v1/jobs/post-preview` queues a strictly dry-run `post` preview, with
`POST /v1/jobs/{id}/cancel` for cancellation and `GET /v1/jobs/{id}` for
status. Jobs run with bounded concurrency and retained history, each holding
a bundle-scoped store lock, stream `queued`, `running`, and terminal states on
`/v1/events` as `jobs` events, and invalidate the query cache when a sandbox
verify updates bundle artifacts. The Detail view renders `Reproduce in
sandbox` and `Preview PR post` with a live drawer only when the server
advertises interactive mode (never in static exports).
