---
"@stainless-code/verdict": minor
---

Publish adjudicated precision and recall beside the strict figures. A
versioned per-run adjudication record reclassifies an unmatched finding as
`real-unlisted`, `matches-ground-truth` (the defect a ground-truth bug names in
words the case's keywords miss, refused when it names a bug the run already
matched or sits outside that bug's window), or `false-positive`. Every entry
names its finding by case, sample, and sig, quotes the code in the rationale,
and names who made the call; ground truth is never edited. `bench/runner
-render <report> -adjudication <record> [-out <path>]` recomputes both figures
without re-running a model, and the report and its Markdown carry the
adjudicated rates, the counts per verdict, the record's path and sha256, and a
per-case keyword-miss list. Strict figures stay untouched, and with no record
the adjudicated block is absent rather than copied from them. A malformed or
contradicted record refuses typed (`bad-adjudication`, `unknown-sig`,
`rationale-required`, `run-mismatch`).
