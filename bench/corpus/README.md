# Seeded benchmark corpus

This directory is the checked-in half of the benchmark corpus: deterministic
seeded cases plus fake agent outputs, used by the `bench-corpus` eval and as
the template for the operator's real-PR corpus. Every case is a real-shaped
review bundle with ground truth:

```
bench/corpus/<case-id>/
  case.json                 # ground truth + provenance (schemas/corpus.schema.json)
  full.diff                 # the reviewed change, with the bug lines in hunks
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

1. Create `bench/corpus/<case-id>/`.
2. Write `full.diff` with at least one hunk covering every bug line and every
   false-positive line the fake agents cite (NEW side).
3. Write `case.json`: `kind: "seeded"`, `provenance.injected` naming the
   defect, and one `bugs[]` entry per known defect (`path`, `line`, `kind`,
   `severity`).
4. Add `agents/<model>.json` per fake model. Keep comment bodies unique per
   bundle: `record` dedupes findings by path + body, not by line.
5. Run `./evals/one.sh bench-corpus` and update the goldens if the new case
   changes the measured rates.

Do not edit `schemas/corpus.schema.json` by hand; run `make schemas`.

The operator runbook for real PRs, real agents, cost estimates, and
publishing measured rates lives in [`docs/benchmark.md`](../../docs/benchmark.md).
