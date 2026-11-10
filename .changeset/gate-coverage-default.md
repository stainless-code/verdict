---
"@stainless-code/verdict": minor
---

Gate on diff coverage by default. `verdict gate` gains a `coverage` dimension
that reads the receipt `post.json`/`record.json` already store for the gated
HEAD and fails when that receipt is absent, names another HEAD, carries no
`coverage[]` attestation (a review that never attested coverage reads
`unknown`, not `pass`), or attests under 100% — the reviewed ranges are not
re-evaluated, so the gate reports the number the store holds. `--coverage
require|warn` sets the posture and defaults to `require`; `--coverage warn`
reports the same shortfall as one `warn:` line and never fails the gate: it is
how to keep the old behavior and the migration while a review attests nothing. An all-exempt
diff passes once a receipt exists, a chunked bundle is graded on the chunk
attestation its receipt stores, and `--coverage` alone is still a usage error.
`post` and `record` keep the warning posture (`--require-coverage` opts into
the refusal), the SDK's `GateRequest` gains the same `Coverage` field with the
same default, and the published envelope schema adds `coverage` to the gate
dimension ids.

A chunked bundle is graded too, because `chunks[]` is its only attestation and
nothing stored that fact. `post.json` and `record.json` gain
`chunk_coverage: {attested, total}`, written beside the existing measured
summary at the point the merge already validates `chunks[]`: the manifest is
the denominator, and only a claim the manifest can name counts toward the
numerator, so the receipt can never attest more than the review wrote. Every
chunk attested passes; a shortfall fails naming the count (`1 of 2 chunk(s)
unattested`); a receipt written by an older binary carries no such key and
still reads `unknown` under `require`. Non-chunked receipts are unchanged byte
for byte.
