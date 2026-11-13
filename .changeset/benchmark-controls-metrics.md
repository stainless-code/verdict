---
"@stainless-code/verdict": minor
---

Make corpus benchmark claims reproducible with typed severity weights,
ordered k-of-n recall, control false-positive rates, weighted F1, signal to
noise, and explicit denominators and observation counts. Preserve unknown
cost and latency as null. The benchmark runner records provenance and command
argv and writes a static Markdown report alongside JSON. Seeded runs are
labelled "not a model-quality claim".

Metric names distinguish false discovery rate from control-PR false-positive
rate. `control_fp_rate` counts controls with any finding; raw counts use
`control_findings_per_case`. Weighted F1 is named
`severity_weighted_pooled_f1` and its pooled formula is documented.
