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

## SZZ candidate mining, replay, and score

`verdict backtest mine --since <duration|ref>` is the offline front half of
06b: it blames each later `fix:` or `Revert "..."` commit's deleted lines back
to the merged or squash PR that introduced them and writes a deterministic
`.verdict/backtest/<run>/mine.json` with zero model calls and zero network.
The artifact is a candidate list, not a corpus, not a measurement, and not
citable: SZZ blame is a hypothesis, every excluded fix is counted with a
reason, and a fix can touch cleanup adjacent to the real bug. Use it to pick
the PRs worth turning into `real` corpus cases by hand, then verify each bug at
the reviewed head before publishing it.

`verdict backtest replay --run <id> --agent <name>` reviews the candidates at
their own `base..head` with the operator's agent and records each review into a
marked local bundle (`.verdict/state/local-bt-*`). `verdict backtest score
--run <id>` then grades those recorded reviews against the lines the miner
blamed, and writes `.verdict/backtest/<run>/report.json`. The score is a pure
read of that run: it launches no agent, builds no bundle, and never writes into
`.verdict/state/corpus-*`. Both replay and score are benchmark input — not
citable, never posted.

### What the backtest numbers mean

Grading reuses the corpus rule (`corpuscore.Evaluate`): a finding matches a bug
when it names the same `path`, its line is within ±1 of the bug's line, and its
text names the defect (the bug's kind in hyphen or space form, or the published
category vocabulary for that kind). A backtest case has no hand-written
keywords: the kind is the miner's deterministic guess, so a finding must name
what that guess calls the defect.

- **`catch_rate = matched / bugs`** over the replayed PRs whose review was
  graded. Read it as a **match rate over SZZ candidates**, not a measured catch
  rate. A bug is visible only when a later fix deleted that exact line: a defect
  fixed without touching its line, or never fixed at all, is invisible, and a
  fix that rewrote the line will usually hide it (`unaligned`). In the other
  direction, a fix that deletes cleanup beside the real bug turns a line that
  was never defective into a candidate, so a match on it is not proof the
  review caught a defect. "Findings on lines that later needed a fix" is the
  numerator, and a hand-verification step is what separates a real catch from a
  candidate that merely matched.
- **`off_target_per_pr = (reported − matched) / prs`.** An unmatched finding is
  not proven false — it may be a real defect on a line no later fix deleted —
  so the report keeps the name "off target" instead of claiming a false
  positive. It measures attention spent away from the blamed lines, not noise.
- **`cost_per_review_usd`** is the priced spend divided by the reviews that
  reported a price; null when none did. `spend.usd` is the sum of the reported
  prices and is a **lower bound** whenever `spend.unpriced_reviews` is non-zero:
  a review that reported no price is unknown spend, never zero.
- **`unclassified`** counts graded bugs whose kind matched no miner rule. A
  finding cannot name such a defect, so those bugs can only lower `catch_rate`;
  the count discloses how much of the denominator is unreachable. It is a
  classifier gap, not a review failure.
- **`signals[]`** splits the same counts by the seed signal, `fix` then
  `revert`. A revert that deletes the line is a stronger signal than a fix
  commit that rewrites it, so the table publishes the two catch rates apart;
  the overall `catch_rate` above stays the pooled rate over every graded bug.
- Every zero denominator renders `null` (`n/a` in the human lines), never `0`.
  `not_replayed` counts mined candidates the run never scored; their bugs stay
  out of every denominator.

The score is a starting measurement for picking corpus cases, not a model
quality claim. To publish one, re-run the chosen cases through the corpus
path above: the SZZ artifact is reproducible and deterministic, but the reviews
are live model runs.

## Index accuracy: callers and `tests-for`

`verdict index callers` and `verdict index tests-for` report edges from a
static index: Go through `go/parser`, TypeScript/JavaScript and Python through
lexical scanners. This section publishes how accurate those edges are against a
**fixture corpus**, so the decision ADR-0003 holds open — a deeper scanner,
`go/types`, or exact rows — rests on a measured number instead of an
impression. The corpus is three small hand-labeled repositories, so every rate
here is a **fixture-corpus rate, not a real-world rate**: it measures what each
scanner model can and cannot see, not what a large repository would cost.

What lives where:

- `internal/core/index/testdata/corpus/{go,ts,py}/` holds the fixture sources
  and, beside each, `expected.json`: every caller site of a queried symbol
  (`path`, `line`, `kind`, and whether a correct index can call it `exact` or
  only `lexical`), the count of unresolved candidates, and every candidate test
  with the mechanism and import depth the walk must report. The Go fixture's
  sources end in `.txt` because the fixture is a module of its own and the
  pre-commit hook vets the directory of every staged `.go` file; the scorecard
  seeds them under their real names.
- `internal/core/index/testdata/corpus/floors.golden` holds the pinned floors,
  one line per scorecard row: the precision and recall a change may not drop
  below.
- `internal/core/index/corpus_test.go` runs the scorecard as
  `TestCorpusAccuracy`. It seeds each repository into a scratch git repository,
  builds the index, prints the table below, and fails when a rate drops below
  its floor, when a labeled path is missing from the fixture, or when the
  build's indexed and skipped counts move.

```sh
go test ./internal/core/index/ -run TestCorpusAccuracy -v        # print the scorecard
go test ./internal/core/index/ -run TestCorpusAccuracy -update   # re-pin the floors
```

`-update` prints the lines that moved, so a re-pin is reviewed like code. The
scanners stay frozen (ADR-0003): a rate is recorded as measured, never raised by
changing the scanner it measures.

### What the rates mean

- Precision is `matched / reported`; recall is `matched / labeled`. Both round
  to four decimals, and a zero denominator renders `n/a`, never `0`.
- A caller row's identity is `path:line kind resolution`. A site reported at the
  right line with the wrong kind or the wrong resolution is a false positive
  _and_ a miss, so the label is the whole claim.
- A Go site inside the declaring package is labeled `exact`. Every
  cross-package site and every site in a scanned language is labeled `lexical`:
  the index matches a name and an import path, never an object.
- `*/callers-cross-package` restricts both sides to sites outside the declaring
  package directory. That is the row the `go/types` gate reads.
- Labels cover call sites and value uses. An import statement or a re-export
  clause is a module edge, not a use, so it is not labeled; the call that goes
  through it is labeled at its own line.
- `unresolved` and `depth_truncated` are pinned counts beside the rows: a
  candidate the index could not attribute and a candidate the depth cap hid.

### The measured scorecard

Measured with `go test ./internal/core/index/ -run TestCorpusAccuracy -v` on the
corpus in this commit. `labeled` is the recall denominator, `reported` the
precision denominator. The block between the markers is pinned: the test
compares it cell by cell, and `-update` republishes it. The counts sit beside
the rates so a deleted label or a rate that rose without a note cannot pass as
an unchanged scorecard.

<!-- scorecard:begin -->

| row                         | precision | recall | matched | reported | labeled |
| --------------------------- | --------- | ------ | ------- | -------- | ------- |
| `go/callers-open`           | 1.0000    | 0.7500 | 6       | 6        | 8       |
| `go/callers-save`           | n/a       | 0.0000 | 0       | 0        | 5       |
| `go/callers-touch`          | n/a       | 0.0000 | 0       | 0        | 1       |
| `go/tests-for-store`        | 1.0000    | 1.0000 | 5       | 5        | 5       |
| `go/tests-for-capped`       | 1.0000    | 1.0000 | 4       | 4        | 4       |
| `go/callers`                | 1.0000    | 0.4286 | 6       | 6        | 14      |
| `go/callers-cross-package`  | 1.0000    | 0.4167 | 5       | 5        | 12      |
| `go/tests-for`              | 1.0000    | 1.0000 | 9       | 9        | 9       |
| `ts/callers-save`           | 1.0000    | 0.5455 | 6       | 6        | 11      |
| `ts/callers-audit`          | 1.0000    | 0.5000 | 1       | 1        | 2       |
| `ts/callers-namesake`       | 1.0000    | 1.0000 | 1       | 1        | 1       |
| `ts/tests-for-save`         | 1.0000    | 1.0000 | 4       | 4        | 4       |
| `ts/tests-for-two-families` | 1.0000    | 0.5000 | 1       | 1        | 2       |
| `ts/callers`                | 1.0000    | 0.5714 | 8       | 8        | 14      |
| `ts/callers-cross-package`  | n/a       | 0.0000 | 0       | 0        | 4       |
| `ts/tests-for`              | 1.0000    | 0.8333 | 5       | 5        | 6       |
| `py/callers-save`           | 1.0000    | 0.4286 | 3       | 3        | 7       |
| `py/callers-write`          | 1.0000    | 1.0000 | 1       | 1        | 1       |
| `py/callers-route`          | 1.0000    | 0.5000 | 1       | 1        | 2       |
| `py/callers-namesake`       | 1.0000    | 1.0000 | 1       | 1        | 1       |
| `py/tests-for-saver`        | 1.0000    | 1.0000 | 5       | 5        | 5       |
| `py/callers`                | 1.0000    | 0.5455 | 6       | 6        | 11      |
| `py/callers-cross-package`  | n/a       | 0.0000 | 0       | 0        | 3       |
| `py/tests-for`              | 1.0000    | 1.0000 | 5       | 5        | 5       |

<!-- scorecard:end -->

### The `go/types` gate

ADR-0003 opens the `go/types` spike only when two arms hold. The corpus arm:
Go **cross-package** caller recall below 0.8, or its precision below 0.9, on
this corpus. The backtest arm: a missed or wrong finding in a real review,
traceable to a Go caller (STA-390). Measured on this corpus:

- precision **1.0000** (5 of 5 reported cross-package sites are real),
- recall **0.4167** (5 of 12 labeled cross-package sites are reported).

The precision arm holds and **the recall arm trips**, so the corpus arm is met.
The backtest arm is pending, so the spike is not opened: the 7 misses below are
a measured gap on a fixture, not yet a finding a review lost.

The 12 labeled sites split three ways, and only the first is reported: a
qualified package call through an import edge (`store.Open`, `st.Open` with an
alias) is a row; a method call on a value (`s.Save`, `a.Touch`, including an
embed and an interface dispatch) is not a row but is counted in `unresolved`,
because only the value's type says which method it carries; and a call through a
dot-import or a re-exported variable is neither a row nor a candidate. Type
information does not settle all three. `go/types` answers a method call on a concrete value
— `s.Save` on a `*store.Store`, `a.Touch` on the promoted `*store.Base` — and
the bare `Open` a dot-import leaves at `internal/dot/dot.go:8`. The re-exported
variable is typed but its origin is a value, so the call at
`internal/cli/cli.go:14` needs value flow, and the interface dispatch at
`internal/app/app.go:14` needs implementation analysis (CHA or RTA) on top.

### What the corpus found

Each item is a measured drop in the table above, not a guess, and each names its
class. A **scanner bug** is a fact the scanner already holds and drops, so fixing
it moves the rate: those are fixed below, each naming the test that pins it, and
ADR-0003's freeze otherwise holds, so no rate rises by tuning a scanner to a
label. A **design limit** is outside what a lexical index resolves: no resolver,
no types, no object identity. A **documented limit** is a rule the manual
states, so the fixture pins the rule rather than a defect.

- **A cross-package method selector was neither a row nor an unresolved
  candidate.** `resolveGoRef` (`internal/core/index/callers.go:300`) answered a
  qualifier-less selector outside the declaring package with a bare `false`.
  Neither `s.Save(1)` in `internal/api/api.go:9` nor `a.Touch()` on an embedded
  `*store.Base` (`internal/app/app.go:18`) was reported or counted: the
  `store.Touch` query answered zero rows at `unresolved` 0, so the envelope
  claimed nothing was left unattributed. `resolveValueSelector` now counts such
  a site in `unresolved` in every package — `store.Touch` answers 0 rows at 1
  unresolved and `store.Save` 0 rows at 5 — and only when the declaration it
  could name is a method, because a value cannot carry a package-level function.
  `TestCallersValueSelectorIsUnresolved` pins both. Rows do not move: the site
  still needs a type. **Scanner bug, fixed in the count.**
- **A dot-import and a re-exported variable lose their calls.** `resolveBareRef`
  (`internal/core/index/callers.go:353`) attributes a bare name only inside the
  declaring package, so `Open("c")` in `internal/dot/dot.go:8` (package `dot`,
  `import . "example.com/m/internal/store"`) is not a caller. A re-exported
  variable is the same shape one step removed: `reexport.Open("e")` in
  `internal/cli/cli.go:14` names `reexport`'s import path, not `store`'s.
  **Design limit.**
- **Scanned-language sites outside the declaring directory are dropped.**
  `resolveScannedRef` (`internal/core/index/callers.go:330`) requires the same
  directory, so every `save(...)` in `ts/app/` and `py/tests/` is invisible to a
  query for the `lib` declaration: cross-package recall is 0.0000 for both
  scanned languages, against 4 and 3 labeled sites. **Design limit.**
- **A scanned language records no value use.** `{ save }` in
  `ts/lib/callers.ts`, `@audit` in `ts/lib/service.ts`, and `@route` in
  `py/lib/views.py` are uses of the symbol, and no row is produced for any of
  them. Only a call or an attribute site is recorded
  (`internal/core/index/scan_ts.go:1267`,
  `internal/core/index/scan_py.go:576`). **Design limit.**
- **An attribute call was attributed to a declaration of the wrong kind.**
  `py/lib/uses.py:6` calls `log.save(value)` on a `Log` that declares `write`
  and no `save`, and the row was reported for `lib.save`, a module function: the
  one false positive the table carried (`py/callers-save` precision 0.7500). The
  scanner knows the callee follows a dot and records the site as `method`
  (`internal/core/index/scan_py.go:587`), and Python also records each
  declaration's kind; resolution now compares the two, so an attribute site
  names a method or a class and never a module-level function. Both precision
  cells are 1.0000 now, pinned by
  `TestCallersPythonRespectsDeclarationKind`. **Scanner bug, fixed.**
- **`imports-module` did not fire for a relative specifier.** `importsAny`
  (`internal/core/index/tests.go:403`) compared the raw specifier with the
  declaring package's import path, so `import { tidy } from "../lib"` in
  `ts/app/tidy.spec.ts` matched neither `"../lib"` nor the resolved `lib`, and
  the file was not a candidate at all. `importedPackage` now resolves each
  specifier by its own language's rule — the same `scannedSpecifier` the other
  mechanisms use — so the file is a candidate at `imports-module, depth 1` and
  `ts/tests-for-save` recall is 1.0000, pinned by
  `TestTestsForRelativeSpecifierImportsPackage`. **Scanner bug, fixed.**
- **A test whose package imports the target was not a candidate.**
  `internal/worker/worker_test.go` imports only `testing`; its package imports
  `internal/store` in `worker.go:3`, which is one hop from the declaring
  package. `transitiveVia` (`internal/core/index/tests.go:357`) started at depth
  2, so the walk counted the hop and discarded the file.
  It is now named `transitive-import, depth 1` — the manual's own mechanism
  definition, with the direct mechanisms still tried first — and a scanned
  specifier naming no indexed directory or file no longer contributes an edge
  to the directory that would have contained it. `go/tests-for-store`,
  `go/tests-for-capped`, and `go/tests-for` recall are all 1.0000, pinned by
  `TestTestsForPackageImportIsOneHop`; `TestTestsForScannedLanguages` carries
  the package-scoped row and `TestTestsForUnresolvedSpecifierIsNoEdge` the
  missing module. **Scanner bug, fixed.**
- **A symbol's `tests-for` language is the first target's.** With `run` declared
  in both `shared/run.py` and `shared/run.ts`, `tests-for --symbol shared.run`
  reads `internal/core/index/tests.go:153` and answers in Python only, so the
  TypeScript test file is never listed (`ts/tests-for-two-families` recall
  0.5000). **Documented limit:** the manual already says a candidate in another
  language family is never listed.

### Coverage and bail-outs

The build reports `files_indexed` and `skipped`, and the corpus pins both per
repository: `go` is 15 indexed and 1 skipped, `ts` 22 and 0, `py` 14 and 0. The
one skip is the Go fixture's `internal/broken/broken.go`, which `go/parser`
refuses, and that is the only bail-out this corpus pins. `indexedFile`
(`internal/core/index/store.go:60`) also counts an oversize file (over the
1 MiB cap) and a file whose references were truncated (over 5,000) as skipped
in every language, and no fixture reaches either bound. What the corpus does
show is that both lexical scanners always finish, recording whatever they
recognized, so a py or ts file the scanner mis-reads appears as a missing edge
and never as a skip. That asymmetry is a gap in the coverage number, not
something the corpus can measure.

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
