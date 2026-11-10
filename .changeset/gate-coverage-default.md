---
"@stainless-code/verdict": minor
---

Gate on diff coverage by default. `verdict gate` gains a `coverage` dimension
that reads the receipt `post.json`/`record.json` already store for the gated
HEAD and fails when that receipt is absent, names another HEAD, or attests
under 100% — the reviewed ranges are not re-evaluated, so the gate reports the
number the store holds. `--coverage require|warn` sets the posture and defaults
to `require`; `--coverage warn` reports the same shortfall as one `warn:` line
and never fails the gate, which is how to keep the old behavior. An all-exempt
diff passes once a receipt exists, a chunked bundle reads `unknown` under
`require` (its `chunks[]` attestation is not stored) and `skip` under `warn`,
and `--coverage` alone is still a usage error. `post` and `record` keep the
warning posture (`--require-coverage` opts into the refusal), the SDK's
`GateRequest` gains the same `Coverage` field with the same default, and the
published envelope schema adds `coverage` to the gate dimension ids.
