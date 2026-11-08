# Landing page claims, and where each one is checked

The homepage at `apps/docs/pages/index.astro` makes a small number of factual
claims. This file is the receipt for every one of them: change a claim, change
its row. It replaces the untracked `.scratch/onboarding/O-landing.md` notes the
components used to cite.

References name `docs/manual.md` sections unless stated; line numbers drift,
so the section name is authoritative.

| Claim on the page                                                                                   | Check against                                                                                                                                                                                                                    |
| --------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| "Verdict splits code review into steps your agent runs on your machine: fetch, review, gate, post." | § Quickstart; the agent writes `review.json`, `report --check` gates, `post` submits (§ Quickstart, § Guarded submission and operator surfaces). Same sentence in `README.md`, `docs/manual.md` and `apps/docs/blume.config.ts`. |
| The skill ships in the binary and writes into every agent it detects                                | § Onboarding (scaffolder + prefs file + skill installer) (`skill install --detect`)                                                                                                                                              |
| `fetch` writes the diff, TOC and roles into `.verdict/state/`                                       | § Quickstart                                                                                                                                                                                                                     |
| `post` checks anchors and submits exactly one review                                                | § Quickstart, § Guarded submission and operator surfaces                                                                                                                                                                         |
| `report --check` exits 1 on open findings at the severity                                           | § Machine surface (`report --check`); envelope shape in `apps/docs/content/build-a-client.mdx` § Exit 1 is not always a refusal                                                                                                  |
| `report --sarif` writes SARIF 2.1.0                                                                 | `cmd/verdict/usage.go:68`; `internal/core/report/sarif.go:36`                                                                                                                                                                    |
| Evidence rungs run `said`, `pointed`, `walked`, `ran`, `reproduced`                                 | § Findings store + triage authority                                                                                                                                                                                              |
| `calibrate` compares a prediction with its thread; `trend` rolls them up                            | § Calibration (predictions vs ground truth), § Trend (portfolio outcomes over time)                                                                                                                                              |
| Trust table: writes, runs, fetches, never                                                           | § Quickstart, § Onboarding (scaffolder + prefs file + skill installer), § Hostile-PR sandbox recipe (opt-in, not enforced), § Child environment; "posts only when `verdict post` runs" in § Quickstart                           |
| Exit codes `0`, `1`, `2`, `3`                                                                       | `docs/build-a-client.mdx` § Exit codes; frozen in `internal/cli/cli.go`                                                                                                                                                          |
| `REVIEW_BUDGET_USD` caps a projected run                                                            | § Cost control (budget, estimate, consent); `--consent-over-budget` in the same section                                                                                                                                          |
| MIT licensed, no subscription, no seats, no charge per review                                       | `LICENSE`; no billing code exists                                                                                                                                                                                                |
| Agent entry points (`llms.txt`, `agent-readability.json`, skill index, `{route}.md`)                | generated into `apps/docs/dist/` by the Blume build; the first three are the footer's `For agents` column                                                                                                                        |
| Pipeline section: stages, their commands, the Shipped and Planned groups, the MCP badge             | `apps/docs/content/roadmap.mdx` § Shipped today and § Next; commands in `verdict --help`; MCP experimental per the roadmap callout; `--since` GitHub-only per `fetch` usage                                                      |
| Vendor prices and the 2026-11-04 date                                                               | `apps/docs/content/comparison.mdx`, the only page that carries them                                                                                                                                                              |

## The terminal sample

`apps/docs/pages/_home/HowItWorks.astro` prints a local-review transcript from
`bin/verdict`, with the current `.verdict/state/` bundle paths; `⋯` marks
elided progress lines. Reproduce the flow with `verdict fetch-local acme/app work`,
`verdict record acme/app work`, then `verdict report acme/app work --check
important`; the sig and progress lines depend on the review text and diff. The
review file was written by hand, the way an agent writes it, and the gate
exited 1.
