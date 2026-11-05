# Seeded benchmark corpus

This directory is the checked-in half of the benchmark corpus: deterministic
seeded cases plus fake agent outputs, used by the `bench-corpus` eval and as
the template for the operator's real-PR corpus. Every case is a real-shaped
review bundle with ground truth:

```
bench/corpus/<case-id>/
  case.json                 # ground truth + provenance (skills/verdict/schemas/corpus.schema.json)
  full.diff                 # real git diff of the reviewed change, with the bug lines in hunks
  snapshots/<file>.before   # the reviewed base tree
  snapshots/<file>.after    # the reviewed head tree
  agents/<model>.json       # a fake agent output (canned review.json)
```

`agents/<model>.json` is metadata plus the exact `review.json` the agent
would have written:

```json
{
  "model": "fixture-good",
  "panel": ["panel-alpha"],
  "review": {
    "schema_version": "1",
    "body": "> ...",
    "approved": false,
    "comments": []
  }
}
```

The run bundle copies `case.json` to `.review/corpus-<run>/corpus.json`, runs
`record`, and keeps `cost.json` plus its `panel` file. `calibrate` grades the
bundle against `corpus.json`; `trend` aggregates every `corpus-*` bundle per
model and panel. No model runs and no network are involved in the checked-in
fixtures.

## Adding a seeded case

1. Create `bench/corpus/<case-id>/` with `snapshots/<file>.before` and
   `snapshots/<file>.after`: the reviewed base and the reviewed head.
2. Generate `full.diff` with real `git diff` between those snapshots — commit
   the before tree, write the after files over it, and capture the diff — so
   `git apply --check` accepts it against the before tree. Use enough context
   that every bug line and every false-positive line the fake agents cite is
   on the NEW side, and take the ground-truth `bugs[].line` from the new-side
   numbers. `go test ./internal/core/corpus/` proves each diff applies,
   reproduces its after tree, and adds every ground-truth line.
3. Write `case.json`: `kind: "seeded"`, `provenance.injected` naming the
   defect, and one `bugs[]` entry per known defect (`path`, `line`, `kind`,
   `severity`).
4. Add `agents/<model>.json` per fake model. Keep comment bodies unique per
   bundle: `record` dedupes findings by path + body, not by line.
5. Run `./evals/one.sh bench-corpus` and update the goldens if the new case
   changes the measured rates.

Do not edit `skills/verdict/schemas/corpus.schema.json` by hand; run `make schemas`.

The operator runbook for real PRs, real agents, cost estimates, and
publishing measured rates lives in [`docs/benchmark.md`](../../docs/benchmark.md).
