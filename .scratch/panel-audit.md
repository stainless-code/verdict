# STA-91 / D27 audit

Base: `7ec5560ef730b0a9bbdd96556412944bebfe5adf`; branch: `agent/panel`.
Read STA-91 and the full Linear spec, including binding scope cut §10, read-only.

## Blocking artifact collision

`internal/core/fetch/bundle.go:208–217` writes the pinned roster as a regular
file `<bundle>/panel`. AC1 requires reading that file; AC2 requires writing only
`panel/plan.json` and `panel/dispatch.md`. A regular file cannot also be their
parent directory. This is current code, not merely an obsolete `.review/` prefix.
A local reproduction with a temporary roster file observed:

    panel/plan.json: NotADirectoryError: errno=20 (Not a directory)

Renaming or deleting the roster during planning violates the two-artifact write
boundary. Changing fetch's roster location changes existing bundles and their
contract; choosing `panel-verification/` instead changes the explicit artifact
contract. No production change made pending orchestration's path decision.
Suggested resolution: keep the pinned roster and use `panel-verification/` for
all three new artifacts, consistently across CLI, schemas, docs and evals.

## Acceptance audit

1. Not implemented. Roster exists (`fetch/bundle.go:208`); no planner verb.
2. Not implemented; blocked by file/directory collision above.
3. Not implemented; no panel verdict schema/loader.
4. Not implemented. `internal/core/stores.go:77` defines FindingEntry without
   verifier results or derived confidence. Shared merge seam is
   `internal/core/record/merge.go:150` (`mergeEntries`).
5. Not implemented; no second-look planner.
6. Existing runs have no verifier dependency; new merge behavior unimplemented.
7. Existing store/envelope contracts untouched in this audit; no new summary.
   Legacy byte identity has not been tested in this run.
8. No verifier evidence enters existing payloads; new summary unimplemented.
9. Not implemented. `internal/provenance/provenance.go:23` hashes exactly four
   files, without verifier.md or validate.md.
10. Ladder not run — this is a blocked audit, with no Go changes.
11. Production non-test changes: zero. Audit notes only.

## Dependency and layout state

D30's ordered wire machinery exists: `stores.go:224` and `stores.go:291` use
ordered objects; new fields must extend these renderers and preserve prior
unknown fields (`record/merge.go:875`), rather than struct-round-tripping.
D14's validate.md prompt is absent from the hashed set. Coordinate a single
prompt-set update; do not claim that D14 has landed.
Current typed configuration replaces `.review/panel` control reads:
`internal/core/fetch/cap_test.go:377` pins the retired file as ignored.
Fetch still emits the flat pinned roster; pinned `run.json` also contains the
resolved configuration (`internal/config/runstate.go:115`). Merely reading
run.json instead does not free the existing `<bundle>/panel` filesystem path.

## Ordered build plan after path resolution

1. Planner + bounded dispatch + CLI registration + literal tests (AC1–2,5).
2. Verdict schema and capped loader; stale, duplicate, unknown/ambiguous sig,
   malformed result tests and mutation proofs (AC3).
3. Shared record/post merge preserving prior results and contested confidence;
   missing-verifier and second-look regression/mutation tests (AC4–7).
4. Post/record/report summaries and one evidence-free body line (AC7–8).
5. Verifier prompt + coordinated hash update, docs, troubleshooting, changeset,
   generated schemas and TypeScript; inspect hash-only golden deltas (AC9).
6. Hermetic CLI plan → invoker verdicts → record eval, full foreground ladder,
   native/Windows lint, byte count and per-AC mutation evidence (AC1–11).

No models, host CLIs, posts, network tests, pushes, or delegates were invoked.
No implementation tests or mutation proofs are claimed.

## Operator decision — blocker resolved

Keep `<bundle>/panel` unchanged. Use `<bundle>/verification/plan.json`,
`verification/dispatch.md`, and `verification/verdicts.json` everywhere.
This explicitly supersedes the spec artifact paths and the suggested
`panel-verification/` name above. The command remains `verdict panel`.
