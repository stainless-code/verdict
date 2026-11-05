---
"@stainless-code/verdict": minor
---

The bench runner learns a repair loop. Pass `-loop N` to let one sample try up to N rounds: the harness runs `record --dry-run --json`, feeds the problem list back, and records only the last valid round. Round evidence lives under `runs/<slug>/round-NN`, the report gains `loops`, `fix_rates`, and `loop_cost`, and `-loop 1` keeps the old single-shot files byte-identical.
