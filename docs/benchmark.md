# Benchmark corpus and measured review rates

`verdict` reports review quality as measured numbers, not a claim: a corpus
of real and seeded pull requests with known bugs, graded offline by
`calibrate` and aggregated by `trend`. This page is the construction rule,
the 06b operator runbook, and the citation contract for the comparison page.

The checked-in starter corpus is [`bench/corpus/`](../bench/corpus/README.md):
eight seeded cases and two fake agent outputs per case. It proves the
reporting path with exact, reproducible numbers; it is not a model-quality
claim.

## What the numbers mean

A corpus bundle is `.verdict/state/corpus-*` holding `corpus.json` (one case
manifest), `findings.json` (the output under test), and the normal
provenance stores (`record.json`, `cost.json`, `panel`). The runner also
writes the reviewed `full.diff` and the `hunks.tsv` index beside it, so the
bundle scores against the hunks the agent actually read. Matching is
deliberately simple and published:

- A reported finding is a **true positive** when it names the same `path`,
  its `line` is within `line_tolerance` lines of the primary line or any
  `also_lines` entry of an as-yet-unmatched ground-truth bug (default 1),
  and its text names that bug's defect. A defect is named by its `kind` in
  hyphen or space form (`off-by-one`, `off by one`), a phrase from the
  published category vocabulary for that kind (see below), or a phrase from
  the bug's optional `keywords`. Matching is case-insensitive and
  whitespace-folded, and a phrase must start a word, so `race` does not
  match inside `trace`.
- A finding that matches through an alternate anchor counts the same bug:
  one bug is one true positive no matter how many of its lines a review
  names, and the reported match keeps the primary line.
- A finding record moved out of its hunk does not count: `record` keeps the
  old path and line but marks it unanchored, and the text is graded as a
  body note, never as a line claim. So the line window can never reach a
  line the reviewed diff does not show.
- Each bug can be matched once. A second finding on the same bug is a
  **false positive**.
- A finding with no path/line (or a reviewer section finding) is a false
  positive. So is a finding in the window that never names the defect: the
  anchor and the claim both have to be right. The tool-generated section
  `record` appends for the comments it moved is not a reviewer finding and is
  not counted.
- A ground-truth bug with no matching finding is **missed**.
- `precision = matched / reported`; `recall = matched / ground_truth`;
  `false_positive_rate = false_positives / reported`. Rates are rounded to
  four decimals; a zero denominator renders `null`, never `0`.

The window is deliberately narrow. On a 4-7-line hunk a `line_tolerance` of
3 makes the whole hunk the same answer, so the number stops measuring
localization; a bug plus/minus one line still lands on the statement that
carries it, and zero rejects a correct anchor on the second line of a
multi-line statement. `TestCheckedInCorpusCases` asserts `line_tolerance` is
smaller than the distance from each anchor (the primary line and every
`also_lines` entry) to the far end of the new-side hunk that shows it, so a
future short hunk cannot quietly make every anchor correct. Every
`also_lines` entry must itself sit on the new side inside a hunk.

The built-in category vocabulary is deliberately small and deterministic:

| `kind`                | phrases                                         |
| --------------------- | ----------------------------------------------- |
| `auth-bypass`         | role, admin, privilege, authorization, bypass   |
| `missing-error-check` | error, short write, unchecked, discarded        |
| `nil-deref`           | nil, deref, null pointer                        |
| `off-by-one`          | one past, loop bound, out of range              |
| `index-out-of-range`  | index, `len(`, out of range                     |
| `path-traversal`      | traversal, confine, escape, `..`                |
| `race`                | race, goroutine, concurrent, wait for the close |
| `injection`           | injection, inject, query argument               |

A finding is only compared with bugs on its own path inside its line window,
so these phrases need only distinguish the category there. A kind outside the
table is named by its kind text and `keywords` alone.

`ground_truth` counts per bundle run, so a case reviewed by two models
contributes its bugs twice to the per-run aggregate. Per-model and per-panel
tables use only that model's or panel's runs. A bundle with multiple cost
models is one run for the case count; run each model in its own bundle for
attributable per-model rates.

## Corpus format

One case, one manifest, published as
[`schemas/corpus.schema.json`](../skills/verdict/schemas/corpus.schema.json):

```json
{
  "schema_version": "1",
  "id": "off-by-one",
  "kind": "seeded",
  "language": "go",
  "line_tolerance": 1,
  "provenance": {
    "kind": "seeded",
    "injected": "Pagination loop advances one element past the slice and ignores the fetch error.",
    "source": "bench/corpus/off-by-one/full.diff"
  },
  "bugs": [
    {
      "path": "pagination.go",
      "line": 42,
      "kind": "off-by-one",
      "severity": "critical",
      "keywords": ["one past", "loop bound", "len(xs)"]
    }
  ]
}
```

- **Seeded** cases name what was injected (`provenance.injected`) and ship a
  deterministic `full.diff`; they are the hermetic starter corpus.
- **Real** cases name the repository, PR number, and the later bug-fix
  commit (`provenance.repo`, `provenance.pr`, `provenance.fix_commit`), plus
  the reviewed head (`provenance.source` may carry the fetch command or
  URL). The ground truth is the defect fixed by that commit, at the line it
  occupied in the reviewed head.
- At least one bug is required. Ground truth is file + line + kind +
  severity; `note` says why the line is a defect. A bug that cannot be
  pointed at a line is not a corpus case.
- `keywords` (optional) is the case's own vocabulary for that defect, in
  case-insensitive, whitespace-folded substring form at a word start. Write
  the words a reviewer would use to describe the defect, not the words you
  want to see: a keyword broad enough to appear in a finding about a
  different bug (`error`, `check`) still only counts on that bug's own path
  and line window, but a keyword nobody would write turns true positives
  into false ones. The bug's `kind` and its category vocabulary always name
  it too, so a case with no keywords is matched on `kind` and category.
- `line_tolerance` (optional, default 1) is the window in lines. Keep it
  short of the far end of the new-side hunk that shows the bug: the corpus
  test refuses a case whose window covers the whole hunk.
- `also_lines` (optional) lists alternate anchors for the same defect: other
  new-side hunk lines where a reviewer can correctly point at this bug. The
  call site and the unconfined join are the worked example: either anchor
  names the same confinement defect. Each entry matches with the same window
  and the same defect-naming rule as the primary line, one bug stays one
  true positive, and the reported match keeps the primary line. List a line
  only when it names the same defect from code in the same hunk; a nearby
  formatting edit is a distractor, not an anchor.
- `benches`/agent outputs stay outside the schema: the checked-in
  `agents/<model>.json` wrapper carries `model`, `panel`, and the exact
  `review.json` body.

## 06a: the hermetic proof

Run the eval:

```sh
make build
./evals/one.sh bench-corpus
```

It assembles all eight cases for both fake models through the existing
`record` verb (16 bundles), then checks:

- every `calibrate acme/bench corpus-… --json` exits 0 and its `data` equals
  `evals/fixtures/bench-corpus/calibrate.golden.jsonl` exactly;
- `trend acme/bench --json` equals
  `evals/fixtures/bench-corpus/trend.golden.json` exactly;
- a `local-*` bundle still refuses the live join with `no-threads` and zero
  `gh` calls, while `corpus-*` succeeds offline;
- a corrupt `corpus.json` refuses typed on `calibrate` and warns/skips on
  `trend`;
- the ordinary portfolio rollup is byte-identical with and without corpus
  bundles, and every fixture byte is unchanged after the reads.

The seeded numbers are exact: overall precision 0.75, recall 0.8333,
false-positive rate 0.25 over 16 runs (18 bug-runs, 20 findings, 15 matched,
5 false positives, 3 missed). `fixture-good` is 0.9/1/0.1; `fixture-noisy` is
0.6/0.6667/0.4. Panel splits are 0.8/0.8/0.2 for `panel-alpha` and
0.7/0.875/0.3 for `panel-beta`. These grade the reporting path, not model
judgment.

## 06b: operator runbook

### 1. Assemble the real cases

Pick merged PRs whose bug was fixed later, on the same repository:

1. Start from a reviewed head `H` (the PR's head at review time), not the
   final merge commit.
2. The later fix commit `F` must change a line that is a real defect at `H`
   — not a style cleanup, rename, or dependency bump. The hunk in `F` gives
   the file and line at `H`; record the exact line where the bad code sits.
3. Record `repo`, `pr`, `fix_commit` (full URL), and the reviewed head SHA.
   If the PR review cited the bug, keep the finding text out of the
   manifest; ground truth stays independent of the reviewer.
4. Prefer bugs an experienced reviewer would call real: off-by-one, nil
   dereference, injection, missing error check, path traversal, race,
   auth bypass, index panic. Exclude anything where "fixed" is debatable.
5. Seed cases follow the same manifest with `kind: "seeded"` and
   `provenance.injected`.

Keep one bug per entry. If a fix commit repairs three independent defects,
either make three entries in one case (same reviewed diff) or three cases if
the code regions are unrelated.

Give each bug the `keywords` a reviewer would use for it (`nil pointer`,
`one past the slice`, `query argument`), taken from the defect itself rather
than from the review under test: the manifest is written before the run, and
a keyword lifted from a specific finding would grade that model against its
own wording. A bug whose only name is its `kind` is still matchable, but
`off-by-one` misses a reviewer who writes "index runs past the end".

### 2. Build one bundle per model

A corpus run never posts and never needs a live thread. Corpus bundles are
marked, local-shaped runs: `record` accepts them, so the merge path is the
same one every local review uses.

```sh
# Review inputs from a normal fetch, kept verbatim.
verdict fetch owner/repo 123
CASE=off-by-one
MODEL=my-model
SLUG="corpus-${CASE}-${MODEL}"

mkdir -p ".verdict/state/$SLUG"
cp ".verdict/state/123/full.diff" ".verdict/state/$SLUG/full.diff"
cp ".verdict/state/123/hunks.tsv" ".verdict/state/$SLUG/hunks.tsv"       # the fetch-time index
cp ".verdict/state/123/review.json" ".verdict/state/$SLUG/review.json"   # the agent output
cp "bench/corpus/$CASE/case.json" ".verdict/state/$SLUG/corpus.json"
printf '{"head_sha":"%s","base_sha":"%s"}\n' "$HEAD" "$BASE" > ".verdict/state/$SLUG/source.json"
printf '%s\n' my-panel > ".verdict/state/$SLUG/panel"              # same panel every run
# cost.json is the agent's normal token report; record stamps the model.
verdict record owner/repo "$SLUG" --json

verdict calibrate owner/repo "$SLUG" --json    # one bundle, full outcomes
verdict trend owner/repo --json                # every bundle, per model/panel
```

Rules: one model per bundle; the same prompt set and panel for every run;
keep the fetched `hunks.tsv` beside the diff — `record` rebuilds a missing
index from `full.diff` and says so, but a bundle without the snapshot has
nothing to hold an anchor to if the diff is ever re-read;
`review.json` must be the raw agent output, not hand-edited to match the
ground truth. A model that misses a bug should show it as `missed`; a model
that reports a non-bug is the false positive the rate is measuring.

For seeded cases, skip `fetch` and use the checked-in `full.diff` and
`agents/<model>.json` review body. Keep the checked-in corpus immutable;
runs live only under `.verdict/state/`.

The runner in [`bench/runner`](../bench/README.md) automates this section:
`-agent` is the model's command line, and each sample gets its own bundle
(`corpus-<case>-<model>-run<n>`), a copied prompt set, and its `record`,
`calibrate`, and `trend` calls. The recipe above stays the reference for what
it does, and for a bundle it cannot assemble.

### 3. Cost estimate per model

The corpus reruns the same review prompt over N cases. Pilot one case per
model, then read the actual spend from `.verdict/state/corpus-*/cost.json`:

```sh
jq -s '[.[] | .models[] | {model: .name, input: (.input_tokens // 0), output: (.output_tokens // 0)}]
       | group_by(.model)
       | map({model: .[0].model, input_tokens: (map(.input)|add), output_tokens: (map(.output)|add)})' \
  .verdict/state/corpus-*/cost.json
```

Project the full run as `pilot_tokens * (N / pilot_cases)` and price it with
the provider's current sheet:

```
est_usd = (input_tokens * input_price + output_tokens * output_price) / 1,000,000
```

Add cache-read and cache-write tokens at their own rates when the report
carries them. State the model, price date, N, and whether the number is
measured (from `cost.json`) or projected (pilot extrapolation). Do not
publish a rate from a run that exceeded the operator's budget; record the
budget in the publication notes.

### 4. Reproducibility

The reporting join is exact: the same manifest and `findings.json` produce
the same rates byte-for-byte (the goldens prove it), and `calibrate`/`trend`
write nothing. Live model reruns are stochastic:

- Run three samples per model, each in its own bundle
  (`corpus-<case>-<model>-run1`, and so on), and publish median, min, and
  max.
- Treat a shift within **±5 percentage points** as the same measurement; a
  larger shift means inspect the prompt set (`prompts_hash`), panel, and
  corpus before publishing.
- Re-measure after a prompt-set change, a model version change, or 90 days;
  stale numbers are marked stale, never cited silently.

### 5. Publish

Publish a checked-in JSON next to the corpus (for example
`bench/results/<UTC-date>.json`) containing:

- the corpus commit SHA and case count;
- the models and panels, with `prompts_hash` and run dates;
- the raw `trend --json` `data.corpus` block and each bundle's calibrate
  `data` (or the command that reproduces them);
- the per-model cost estimate and whether it is measured or projected;
- the three-run spread and the ±5-point tolerance.

`bench/runner` writes this file when it is run without `-internal`; then link
it from the model comparison page.

## Loop mode

`-loop N` runs a repair loop per sample. The default is `1`, which keeps the
single-shot bytes. The runbook pilot value is `3`. The max is `5`. The sample
timeout is split across rounds with a 60 second floor per round.

The harness plays verdict. Each round the model writes `review.json`, the
harness runs `record --dry-run --json`, and on problems it feeds the next
round the prior review bytes verbatim plus the envelope `error.problems` verbatim plus the harness lines verbatim,
with the round number and the remaining budget. Round 1 keeps the single-shot
prompt. Later rounds append the prior review and one feedback fence with the same run nonce. Prior
model output and feedback stay untrusted. The fence never grants execution.

A non-JSON final message from `pi.sh` never reaches dry run. The adapter
refuses before it writes `review.json`, so the harness counts an adapter
failure with the raw output in `agent.log`. A timeout or a missing review
ends the same way, with wall time and any reported spend kept.

Round evidence lives under `runs/<slug>/round-NN` with `prompt.md`,
`review.json`, `dryrun.json`, `dryrun.stderr.txt`, `normalized.json`,
`cost.json`, and `agent.log`. The final bundle is written once from the last
valid round. When `-loop` is `1`, the report omits `loops`, `fix_rates`, and
`loop_cost`, so the file stays byte-comparable.

The report adds one loop summary per sample with valid on first try, rounds
to valid, final validity, give-up, added and dropped finding ids, round 1
and final problem lists, and per-round walls. Calibrate rates are published
for the final bundle only. An invalid round 1 has no findings file, so no
precision is invented for it. A warning such as `rigor-unset` does not refuse.
Deletion to clear a problem counts as dropped, never as a fix. A model fix
needs changed review bytes and no repair for that code on that path. Repairs
count separately. Fix rates split error and warning levels per code. Costs sum
every attempt including invalid and adapter-failed rounds when spend is
present. Missing spend stays unmeasured, never zero. Model attribution is
preserved. Loop runs are never citable.

## SZZ candidate mining

`verdict backtest mine --since <duration|ref>` is the offline front half of
06b: it blames each later `fix:` or `Revert "..."` commit's deleted lines back
to the merged or squash PR that introduced them and writes a deterministic
`.verdict/backtest/<run>/mine.json` with zero model calls and zero network.
The artifact is a candidate list, not a corpus, not a measurement, and not
citable: SZZ blame is a hypothesis, every excluded fix is counted with a
reason, and replay/score are later slices. Use it to pick the PRs worth
turning into `real` corpus cases by hand, then verify each bug at the reviewed
head before publishing it.

## What the comparison page may cite

Once 06b has run, the comparison page may cite:

- overall precision, recall, and false-positive rate with the denominators
  (`cases`, bug-runs, reported findings) and the corpus commit/date;
- the per-model and per-panel table from `data.corpus`, including the
  reproducibility spread;
- the cost note per model, labelled measured or projected;
- a link to the published JSON and to this runbook.

It may not cite "same review depth", "better findings", or any equivalence
claim without a linked corpus number. Claims with no data stay removed or become
explicitly stated opinion. Do not cherry-pick a subset of cases: the
published block is all runs, and a subset is a new publication with its own
denominators and rationale.

Until 06b publishes a measurement, the comparison page has no measured
claim to cite; keep unmeasured equivalence wording softened or removed.
