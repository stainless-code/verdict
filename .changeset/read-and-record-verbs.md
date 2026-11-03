---
"@stainless-code/verdict": patch
---

`record` and the read-back verbs ship in the `verdict` binary. `record`
validates a local `review.json` and merges it into `findings.json` +
`record.json` with no network and no post; `report` reads the store back as
ranked findings, verdict, counts, SARIF, and a spend rollup, with `--check` and
`--completeness` as opt-in machine gates; `next` prints the top actionable
finding with the command that acts on it; `show` prints one full record;
`ready` reports the CI, thread, and description blockers that stand between a
PR and a usable review; `trend` rolls a portfolio up over the stores and audits
knowledge-base overlap; `calibrate` joins findings against live thread state
and the files changed since the reviewed head, including an offline `--probe`
that grades closed findings against their triage dispositions; `doctor`
preflights `gh` auth and store writability; `check-tree` refuses on unexpected
dirt. `record`, `report`, `next`, and `show` read any provider bundle; `ready`
and `calibrate` need a GitHub PR and refuse the rest typed
(`provider-unimplemented`).
