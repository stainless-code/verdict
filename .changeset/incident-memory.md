---
"@stainless-code/verdict": minor
---

`verdict learn --incident <bundle> --sha <merge> --path <path>` attaches one
revert or incident to a recorded bundle, writing
`.verdict/incidents.jsonl` at the repo root: one row per `(bundle, sha)` pair
with `{schema_version, bundle, sha, paths, note, url, detected_at}`, kept
sorted by bundle then sha. `--path` repeats (sorted-deduped), `--note` caps at
2,000 bytes, `--url` takes `http(s)` only, `--at` defaults to now, and the
bundle must already carry `findings.json`. The attach is idempotent — the
second run reports `existing`, moves no byte, and answers with the stored row —
and `--dry-run` reports what the write would do, writing nothing.

`verdict trend --incidents` joins that ledger to the bundles' findings stores
and reports `incidents`, `measured`, `unmeasured`, `hit`, `missed`, `miss_rate`
(`null` when nothing measured), and per-bundle rows: an incident is a hit when
one non-`false-positive` finding covers one of its paths, exact or as a
directory prefix. A bundle whose store will not read is unmeasured rather than
a miss; a corrupt ledger refuses `corrupt-store`; no ledger reads a zero tally.
The unflagged output stays byte-identical.

The flag joins the CLI, MCP (`learn` gains `incident`/`sha`/`path`/`note`/
`url`/`at`, `trend` gains `incidents`), Go SDK (`pkg/verdict`), the TypeScript
client (`clients/ts`), and the published JSON Schemas
(`learnIncident`, `trendIncidents`, `trendIncidentBundle`).

Attaches are serialized by an OS advisory lock on
`.verdict/incidents.jsonl.lock`: a second attach waits up to five seconds and
refuses `store-busy`, while a crashed holder releases the lock at once. The
lock file persists and is ignored — the tracked ignore contract
(`.verdict/.gitignore`, written by `init` and kept current by the attach) gains
the `incidents.jsonl.lock` line.

An attach that would push the ledger past the 8 MiB the reader opens refuses
`ledger-too-large` and writes nothing, instead of leaving a file no later read
(attach or `trend --incidents`) can open.
