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

A corpus bundle is `.review/corpus-*` holding `corpus.json` (one case
manifest), `findings.json` (the output under test), and the normal
provenance stores (`record.json`, `cost.json`, `panel`). Matching is
deliberately simple and published:

- A reported finding is a **true positive** when it names the same `path`
  and its `line` is within `line_tolerance` lines of an as-yet-unmatched
  ground-truth bug (default 3). Kind and severity are recorded ground truth,
  not part of the match.
- Each bug can be matched once. A second finding on the same bug is a
  **false positive**.
- A finding with no path/line (or a section finding) is a false positive.
- A ground-truth bug with no matching finding is **missed**.
- `precision = matched / reported`; `recall = matched / ground_truth`;
  `false_positive_rate = false_positives / reported`. Rates are rounded to
  four decimals; a zero denominator renders `null`, never `0`.

`ground_truth` counts per bundle run, so a case reviewed by two models
contributes its bugs twice to the per-run aggregate. Per-model and per-panel
tables use only that model's or panel's runs. A bundle with multiple cost
models is one run for the case count; run each model in its own bundle for
attributable per-model rates.

## Corpus format

One case, one manifest, published as
[`schemas/corpus.schema.json`](../schemas/corpus.schema.json):

```json
{
  "schema_version": "1",
  "id": "off-by-one",
  "kind": "seeded",
  "language": "go",
  "line_tolerance": 3,
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
      "severity": "critical"
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

mkdir -p ".review/$SLUG"
cp ".review/123/full.diff" ".review/$SLUG/full.diff"
cp ".review/123/review.json" ".review/$SLUG/review.json"   # the agent output
cp "bench/corpus/$CASE/case.json" ".review/$SLUG/corpus.json"
printf '{"head_sha":"%s","base_sha":"%s"}\n' "$HEAD" "$BASE" > ".review/$SLUG/source.json"
printf '%s\n' my-panel > ".review/$SLUG/panel"              # same panel every run
# cost.json is the agent's normal token report; record stamps the model.
verdict record owner/repo "$SLUG" --json

verdict calibrate owner/repo "$SLUG" --json    # one bundle, full outcomes
verdict trend owner/repo --json                # every bundle, per model/panel
```

Rules: one model per bundle; the same prompt set and panel for every run;
`review.json` must be the raw agent output, not hand-edited to match the
ground truth. A model that misses a bug should show it as `missed`; a model
that reports a non-bug is the false positive the rate is measuring.

For seeded cases, skip `fetch` and use the checked-in `full.diff` and
`agents/<model>.json` review body. Keep the checked-in corpus immutable;
runs live only under `.review/`.

### 3. Cost estimate per model

The corpus reruns the same review prompt over N cases. Pilot one case per
model, then read the actual spend from `.review/corpus-*/cost.json`:

```sh
jq -s '[.[] | .models[] | {model: .name, input: (.input_tokens // 0), output: (.output_tokens // 0)}]
       | group_by(.model)
       | map({model: .[0].model, input_tokens: (map(.input)|add), output_tokens: (map(.output)|add)})' \
  .review/corpus-*/cost.json
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

Then update the comparison page (ticket 07) to link that file.

## What ticket 07 may cite

Once 06b has run, the comparison page may cite:

- overall precision, recall, and false-positive rate with the denominators
  (`cases`, bug-runs, reported findings) and the corpus commit/date;
- the per-model and per-panel table from `data.corpus`, including the
  reproducibility spread;
- the cost note per model, labelled measured or projected;
- a link to the published JSON and to this runbook.

It may not cite "same review depth", "better findings", or any parity claim
without a linked corpus number. Claims with no data stay removed or become
explicitly stated opinion. Do not cherry-pick a subset of cases: the
published block is all runs, and a subset is a new publication with its own
denominators and rationale.

Until 06b publishes a measurement, the comparison page has no measured
claim to cite; keep unmeasured parity wording softened or removed.
