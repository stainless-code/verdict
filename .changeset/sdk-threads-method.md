---
"@stainless-code/verdict": minor
---

The Go SDK and the TypeScript client can read review-thread state in-process,
so the one review read an embedder had to shell out for is now a method.
`verdict.Threads` wraps the `threads` verb: `ThreadsRequest` carries the repo,
PR, and `--provider` override, and `ThreadsResponse` returns the envelope plus
the refreshed feed tally (`Dir`, `File`, `Provider`, `HeadSHA`, `FetchedAt`,
`Truncated`, `Threads`, `Open`, `Outdated`, `Verdict`), with `Data`
byte-identical to `threads --json`. The TS client gains `threads(...)`
resolving `Envelope<DataFor["threads"]>`, and every review verb now has a typed
client method instead of falling back to `run()`. No wire or behavior change:
the verb, its data profile, and its MCP tool already existed.
