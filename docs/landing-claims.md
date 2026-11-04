# Landing page claims, and where each one is checked

The homepage at `apps/docs/pages/index.astro` makes a small number of factual
claims. This file is the receipt for every one of them: change a claim, change
its row. It replaces the untracked `.scratch/onboarding/O-landing.md` notes the
components used to cite.

Line numbers are `docs/manual.md` unless stated.

| Claim on the page                                                                                   | Check against                                                                                                                                                                                             |
| --------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| "Verdict splits code review into steps your agent runs on your machine: fetch, review, gate, post." | Quickstart (manual:6-30); the agent writes `review.json`, `report --check` gates, `post` submits (manual:23-27, 516-517). Same sentence in `README.md`, `docs/manual.md` and `apps/docs/blume.config.ts`. |
| The skill ships in the binary and writes into every agent it detects                                | manual:948-955 (`skill install --detect`)                                                                                                                                                                 |
| `fetch` writes the diff, TOC and roles into `.review/`                                              | manual:20-23                                                                                                                                                                                              |
| `post` checks anchors and submits exactly one review                                                | manual:25-27, 106-107                                                                                                                                                                                     |
| `report --check` exits 1 on open findings at the severity                                           | manual:516-517; `report --check` envelope shape in `apps/docs/content/build-a-client.mdx` § Exit 1 is not always a refusal                                                                                |
| `report --sarif` writes SARIF 2.1.0                                                                 | `cmd/verdict/usage.go:68`; `internal/core/report/sarif.go:36`                                                                                                                                             |
| Evidence rungs run `said`, `pointed`, `walked`, `ran`, `reproduced`                                 | manual:103, 238                                                                                                                                                                                           |
| `calibrate` compares a prediction with its thread; `trend` rolls them up                            | manual:158, 161, 277                                                                                                                                                                                      |
| Trust table: writes, runs, fetches, never                                                           | writes manual:20-23, 948-955; `gh` and `git` manual:4; hosts manual:189, 1412; "posts only when `verdict post` runs" manual:25-27                                                                         |
| Exit codes `0`, `1`, `2`, `3`                                                                       | `docs/build-a-client.mdx` § Exit codes; frozen in `internal/cli/cli.go`                                                                                                                                   |
| `REVIEW_BUDGET_USD` caps a projected run                                                            | manual:689; `--consent-over-budget` in the same section                                                                                                                                                   |
| MIT licensed, no subscription, no seats, no charge per review                                       | `LICENSE`; no billing code exists                                                                                                                                                                         |
| Agent entry points (`llms.txt`, `agent-readability.json`, skill index, `{route}.md`)                | generated into `apps/docs/dist/` by the Blume build; the first three are the footer's `For agents` column                                                                                                 |
| Pipeline section: stages, their commands, the Shipped and Planned groups, the MCP badge             | `apps/docs/content/roadmap.mdx` § Shipped in 0.4.0 and § Next; commands in `verdict --help`; MCP experimental per the roadmap callout; `--since` GitHub-only per `fetch` usage                            |
| Vendor prices and the 2026-11-04 date                                                               | `apps/docs/content/comparison.mdx`, the only page that carries them                                                                                                                                       |

## The terminal sample

`apps/docs/pages/_home/HowItWorks.astro` prints output from `bin/verdict`
0.4.0, run in a scratch git repo whose `src/retry.ts` had a two-line change;
`⋯` marks elided progress lines and nothing else was edited. Reproduce it with
`verdict fetch-local acme/app work`, `verdict record acme/app work`, then
`verdict report acme/app work --check important`. The review file was written by
hand, the way an agent writes it, and the gate exited 1.
