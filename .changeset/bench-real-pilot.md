---
"@stainless-code/verdict": minor
---

Add the first four real benchmark cases (urfave/cli: three bugs and one clean
control) and harden the benchmark runner for paid runs.

Breaking: real corpus cases now require `provenance.base_sha` and
`provenance.head_sha` (40-hex), the commits the reviewed diff spans. A bundle
stamps the same pair, so a case names exactly what was reviewed. Older real
cases without them fail validation.

`bench/runner -max-usd <n>` stops scheduling once measured spend reaches `n`,
lists every unrun sample in `skipped`, and marks the run uncitable. The cost
rollup counts refused samples (`cost.refused_samples`, `cost.refused_usd`)
instead of hiding them. A case's ground-truth keywords must not appear in its
diff, and every ground-truth line, `also_lines` included, must be an added line.
