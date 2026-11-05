---
"@stainless-code/verdict": patch
---

`calibrate` and `trend` grade a corpus finding on the defect it names, not the
line it picked. A finding used to count as a true positive whenever its path
matched and its line sat within `line_tolerance` of a known bug, so on a
four-to-seven-line hunk every anchor was correct and the reported precision
measured line-guessing rather than review quality; the same line could hold a
comment about variable naming and score as a catch. A true positive now also
has to name the bug: one of its `keywords` (new, optional in
`corpus.schema.json`), its `kind`, or a phrase from the published category
vocabulary. The default window drops from three lines to one, and a finding `record` moved out of its hunk is
never a true positive, so the anchor is measured and not assumed.

The human false-positive line says which rule failed, so a run distinguishes a
finding that landed off the bug from one that landed on it and described
something else. Corpus bundles the benchmark runner writes now carry the
reviewed `full.diff` and its `hunks.tsv`, so `record` scores against the hunks
the agent read instead of rebuilding an index from a diff that may have moved.
