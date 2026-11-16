---
"@stainless-code/verdict": patch
---

**Fixes**

- **Read-verb refusals** — `report`, `next`, and `show` refusals carry a shell-quoted `re-enter:` line naming the invocation you typed. [Machine surface](docs/manual.md#machine-surface) <!-- changesets: read-verb-re-enter.md -->
- **`next --coverage` with no positionals** — leads with one `error:` line naming what is missing, and a plain `Basis:` line is accepted without a warning. [Machine surface](docs/manual.md#machine-surface) <!-- changesets: next-usage-basis-plain.md -->
- **Unnamed cost models** — `record` warns once per `cost.json` `models[]` entry with no `name`, and still accepts and prices the record. [Cost control](docs/manual.md#cost-control-budget-estimate-consent) <!-- changesets: record-cost-name-warnings.md -->
- **Over-cap and missing-index refusals** — `diff-too-large` and `missing-index` now carry a `next:` line naming the exclusions that fit, and the exact build command. [Over-cap diffs](docs/manual.md#over-cap-diffs-chunked-fetch--manual-recipe) <!-- changesets: fetch-refusal-next-steps.md -->
- **Five small repairs** — an agent that fails just before its budget reads `agent-failed`, MCP refusals carry their warnings, `index-capped` names the bytes it measured, `index status` points at the query, and a crashed agent's partial review can no longer block on a FIFO. [Machine surface](docs/manual.md#machine-surface) <!-- changesets: review-residuals.md -->
- **Windows watchdog** — a command that failed to start and survived the cleanup kill now returns at once, naming both errors, instead of hanging forever. [Bounded foreground waits](docs/manual.md#bounded-foreground-waits) <!-- changesets: windows-watchdog-no-hang.md -->
- **Quoted upgrade hints** — `verdict upgrade` quotes its project-local `cd` hint so a path with a space or quote pastes resolvably, and the uninstall sibling hint reads as a sentence. [Machine surface](docs/manual.md#machine-surface) <!-- changesets: upgrade-hints-quoting.md -->
- **Release asset check** — the release script's completeness check no longer reports a shipped asset as missing when the asset list is large. [Release train](CONTRIBUTING.md) <!-- changesets: release-asset-sigpipe.md -->
- **`pr-wait` inconsistent reads** — a head that is not yet the same on both APIs is retried instead of failing the bounded wait, and `inconsistent_observations` keeps the retry visible. [Bounded foreground waits](docs/manual.md#bounded-foreground-waits) <!-- changesets: pr-wait-inconsistent-reads.md -->
- **Windows watchdog resume** — a command that failed before starting under load retries the thread lookup instead of refusing `resume suspended verify process`. [Bounded foreground waits](docs/manual.md#bounded-foreground-waits) <!-- changesets: windows-watchdog-resume-retry.md -->
- **`backtest mine` in a partial clone** — a `--filter=blob:none` clone refuses `partial-clone` with the step that fetches the missing objects, instead of stalling. [Backtest](docs/manual.md#backtest-szz-candidates) <!-- changesets: backtest-partial-clone.md -->
- **Quiet local live skip** — a local bundle's live recheck writes one `info:` line to the human stream instead of a warning or a `--json` `warnings[]` entry; failed remote reads still warn. [Machine surface](docs/manual.md#machine-surface) <!-- changesets: sta-425-local-skip-info.md -->
- **Optional review schema version** — `record` accepts a `review.json` without `schema_version` and reads it as version 1, while a present value other than `"1"` still refuses. [Review-input problems](docs/manual.md#review-input-problems) <!-- changesets: sta-425-optional-schema-version.md -->
