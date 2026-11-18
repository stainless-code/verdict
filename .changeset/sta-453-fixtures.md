---
"@stainless-code/verdict": minor
---

`backtest fixtures` grades converted fixture recordings against hand-known defects in a new fixture corpus. It publishes recall, bonus-union, false-green, hallucination (with explicit `unverifiable` counts) and `review-insufficient` metrics, reproduces seven hand scores as calibration, and writes one report under `.verdict/backtest/fixtures/report.json`. The mode never launches an agent or calls a host.
