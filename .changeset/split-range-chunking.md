---
"@stainless-code/verdict": minor
---

Chunked split-range reviews. Very large PRs are reviewable again: with
`--chunk` (or `.review/chunk`) an over-cap diff is sliced on file boundaries
into numbered chunks with a versioned manifest, the agent reviews one chunk at
a time, and the results synthesize into one verdict with a summed Basis line.
`post` and `record` refuse coverage that claims to be complete but is attested
partial, and warn loudly when a chunked review is unattested. `fetch-local`
supports the same flow.
