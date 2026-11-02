---
"@stainless-code/verdict": minor
---

Chunked split-range reviews. Over-cap diffs machine-slice on file boundaries (`--chunk` / `.review/chunk` opt-in) with a versioned manifest; the Review contract reads per-chunk and synthesizes one verdict with a summed Basis line; `post`/`record` refuse attested-but-partial coverage and warn loudly on unattested chunked reviews. Same flow on `fetch-local`.
