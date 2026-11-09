---
"@stainless-code/verdict": minor
---

Attest diff coverage. `review.json` gains an optional `coverage[]` of
`{file, ranges?}`, checked against the bundle's own `hunks.tsv`: `post` and
`record` warn once and add one body line on a partial attestation, and the new
`--require-coverage` flag refuses `coverage-incomplete` without bypassing
`--force`. `next --coverage` reports the unattested ranges before recording, and
`post.json`/`record.json` carry the measured summary. Generated and vendored
paths are exempt; chunked bundles keep `chunks[]` as their only attestation.
Legacy reviews render byte-identically.
