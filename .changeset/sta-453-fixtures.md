---
"@stainless-code/verdict": minor
---

`backtest fixtures` grades archived reviews against hand-known defects in a new fixture corpus. It publishes recall, bonus-union, false-green, hallucination and `review-insufficient` metrics, reproduces seven hand scores as calibration, and writes one report under `.verdict/backtest/fixtures/report.json`. The mode never launches an agent or calls a host.
