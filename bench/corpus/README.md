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

The run bundle copies `case.json` to `.verdict/state/corpus-<run>/corpus.json`, runs
`record`, and keeps `cost.json` plus its `panel` file. `calibrate` grades the
bundle against `corpus.json`; `trend` aggregates every `corpus-*` bundle per
model and panel. No model runs and no network are involved in the checked-in
fixtures.

A finding counts for a bug when it is on the same path, within
`line_tolerance` lines (default 1) of the bug's `line` or any of its
`also_lines` alternate anchors, and its `body` names the defect — one of
the bug's `keywords`, its `kind`, or a phrase from the published category
vocabulary in `docs/benchmark.md`. Matching any anchor counts the bug once
and reports the primary line. A finding `record` moved out of its hunk
is a body note and never counts. The checked-in fake reviews do exactly that,
and `TestCheckedInCorpusCases` fails if a checked-in case's tolerance is not
narrower than every anchor's hunk or no fake output names a bug.

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
   `severity`, and the `keywords` a reviewer would use for it). When one
   defect has two defensible anchors in the same hunk, keep the primary
   `line` on the statement that carries the defect and list the other in
   `also_lines`; a nearby edit that only rewords or reformats is a
   distractor, not an anchor.
4. Add `agents/<model>.json` per fake model. Keep comment bodies unique per
   bundle: `record` dedupes findings by path + body, not by line.
5. Run `./evals/one.sh bench-corpus` and update the goldens if the new case
   changes the measured rates.

## Adding a real case

A real case is one merged PR at the head a review would have seen, plus the
later commit that deleted its defect; a real control is the same without a
defect. It carries no fixtures and no snapshot tree, because the repository
at the recorded head is the tree:

```
bench/corpus/<repo>-<pr>-<slug>/
  case.json                 # kind: real (or control), provenance.repo/.pr/.base_sha/.head_sha/.fix_commit
  full.diff                 # git diff <base> <reviewed head>, verbatim
  hunks.tsv                 # the index of that diff, as fetch writes one
```

- `hunks.tsv` is checked in for reading and for drift: the runner rebuilds the
  index from `full.diff` for every bundle, so a stale copy would only mislead a
  reader of the case directory.
- `provenance.base_sha` and `provenance.head_sha` are the 40-hex commits the
  `full.diff` spans; a bundle stamps them, so the diff a model reviews names the
  two commits a reader can check it against. `provenance.fix_commit` is the full
  URL of the later fix, and a control omits it: it has no defect that was
  fixed.
- `bugs[]` ground truth is a line of the reviewed head inside one of the
  diff's hunks (the added line itself, or the statement the fix deletes), with
  `kind`, `severity`, and the `keywords` a reviewer would use. A fix that also
  shows the call site lists it in `also_lines`.
- A keyword must name the defect in words. Validation refuses one that the
  case's own `full.diff` spells (case-insensitively, as a whole identifier or
  word), because grading on the patch's vocabulary is a free pass: a finding
  that says "`StopOnNthArg` is a new field" names the change, not the bug, and
  a keyword lifted from the diff grades it as a real report.
- No `snapshots/` and no `agents/`: `TestSeededDiffApplies` skips a real case
  (its applied diff is proven by the repository it came from), and the
  `bench-corpus` eval records bundles only for cases with fixture outputs, so a
  real case never enters the hermetic rates. Real cases are scored by live runs
  under `.verdict/state/`, as `docs/benchmark.md` §2 describes.

Do not edit `skills/verdict/schemas/corpus.schema.json` by hand; run `make schemas`.

The operator runbook for real PRs, real agents, cost estimates, and
publishing measured rates lives in [`docs/benchmark.md`](../../docs/benchmark.md).
