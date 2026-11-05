# The benchmark runner

`bench/runner` is a dev tool that drives one agent over the corpus and scores
it with the shipped binary: the same `record`, `calibrate`, and `trend` verbs
[`docs/benchmark.md`](../docs/benchmark.md) has an operator run by hand. It
produces the publication JSON that runbook asks for.

It is not shipped. Nothing in the release build, `release/contract.json`, or
goreleaser imports `bench/`, so `make size` does not move.

## Run one

```sh
make build
go run ./bench/runner -model fixture-good -runs 3 -internal \
  -agent 'sh "$VERDICT_BENCH_ROOT/bench/agents/fixture.sh"'
```

That plays the checked-in fixture reviews over all eight seeded cases, three
samples each, and writes `bench/results/pilot/<date>-<model>-pilot.json`. The
fixture agent never reads the prompt: a run through it proves the runner and
the scoring path, never model judgment, and it spends nothing.

Add `BENCH_FIXTURE=fixture-noisy` to run the over-reporting stub.

## Point it at a model

`-agent` is a command line handed to `sh -c` once per sample, with the run
directory as its working directory: a relative path in it resolves against the
run dir, so reach the checkout through `VERDICT_BENCH_ROOT` or a `PATH`
command. The runner sets:

| variable                                                                                  | meaning                                                                                              |
| ----------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------- |
| `VERDICT_BENCH_PROMPT`                                                                    | the prompt to read (trusted half, then one nonce fence per bundle file)                              |
| `VERDICT_BENCH_OUT`                                                                       | where to write `review.json` (required; the sample fails without it)                                 |
| `VERDICT_BENCH_COST`                                                                      | where to write the spend record (optional; the runner synthesizes `v1`/`standard` without `est_usd`) |
| `VERDICT_BENCH_ROOT`                                                                      | the checkout, so an agent command can name its scripts without a machine-specific path               |
| `VERDICT_BENCH_CASE`, `VERDICT_BENCH_RUN`, `VERDICT_BENCH_MODEL`, `VERDICT_BENCH_WORKDIR` | context for logging                                                                                  |

```sh
go run ./bench/runner \
  -model my-model -panel my-panel -runs 3 \
  -agent 'my-agent --prompt "$VERDICT_BENCH_PROMPT" --out "$VERDICT_BENCH_OUT" --cost "$VERDICT_BENCH_COST"'
```

Everything else is derived, so a run cannot drift from its corpus: the model
comes from `-model` and is verified twice (the bundle's `record.json` and the
`calibrate` block must both name it), the panel is pinned in `.review/panel`,
and the prompt set is copied into the run dir so `prompts_hash` names what the
agents read.

## Run it with a real model

`bench/agents/pi.sh` is the adapter for [`pi`](https://github.com/earendil-works/pi):
it reads `$VERDICT_BENCH_PROMPT`, hands it to one non-interactive `pi` run, and
writes the review and its spend where the runner expects them.

```sh
make build
go run ./bench/runner -model deepseek/deepseek-flash -runs 3 -internal \
  -agent 'sh "$VERDICT_BENCH_ROOT/bench/agents/pi.sh"'
```

It pins `--mode json -p --no-session --no-tools --no-mcp --no-skills
--no-prompt-templates --no-context-files`, so the sample reads the prompt and
nothing else: no repository, no local skills, no session of yours. The prompt
file arrives as one `@file` argument plus one line naming `$VERDICT_BENCH_OUT`.
The last assistant message is the review — the adapter strips a code fence when
the model adds one, refuses anything that is not JSON, and exits non-zero so the
runner records a failure instead of scoring garbage.

`pi`'s default system prompt advertises tools, and this adapter disables them. A
model told it can run commands answers with literal tool-call markup instead of
a review (4 of 24 samples did before `--system-prompt` replaced that claim with
the truth). The shipped prompt set is written for a harness with tools and
subagents, so a tool-free sample reads that workflow without being able to run
it: that is the measurement, not something to paper over.

| variable         | default                        |
| ---------------- | ------------------------------ |
| `BENCH_PI_MODEL` | `deepseek/deepseek-flash:high` |
| `BENCH_PI_BIN`   | `pi` on `PATH`                 |

`BENCH_PI_MODEL` is a `provider/model:thinking` pattern. A bundle is attributed
to a bare model id, so the adapter refuses to run when the pattern's model is
not exactly the `-model` the runner was given: otherwise the spend splits across
two names and the rates stop describing one model.

Cost is measured, not estimated: `pi --mode json` reports provider usage per
assistant message, including `cost.total`, and the adapter sums those into
`cost.json` with `models[0].name` set to `$VERDICT_BENCH_MODEL`. When the
provider reports no price it omits `est_usd`, which reads `unmeasured` in the
report rather than free.

[`bench/results/pilot/2026-11-04-deepseek-flash-pilot.json`](results/pilot/2026-11-04-deepseek-flash-pilot.json)
is the first such run: eight cases, three samples each, `internal`, never
citable.

## Flags

| flag        | default                               |                                                               |
| ----------- | ------------------------------------- | ------------------------------------------------------------- |
| `-model`    | —                                     | model id every bundle is attributed to (required)             |
| `-agent`    | —                                     | agent command line (required)                                 |
| `-panel`    | `bench-<model>`                       | panel the samples run under                                   |
| `-cases`    | the whole corpus                      | comma-separated case ids; a subset is never citable           |
| `-runs`     | `1`                                   | samples per case; the runbook publishes `3`                   |
| `-timeout`  | `15m`                                 | budget for one sample                                         |
| `-root`     | the module root                       | checkout to read the corpus, prompts, and binary from         |
| `-corpus`   | `<root>/bench/corpus`                 | corpus directory                                              |
| `-verdict`  | `<root>/bin/verdict`                  | the binary that scores                                        |
| `-workdir`  | a fresh temp dir                      | run directory; must be empty or absent                        |
| `-out`      | `bench/results[/pilot]/<run-id>.json` | publication JSON                                              |
| `-internal` | `false`                               | mark a pilot: never citable, published under `results/pilot/` |
| `-repo`     | `acme/bench`                          | owner/repo label the corpus verbs take (local, never network) |

## What it writes

`-workdir` holds the evidence: `runs/<slug>/prompt.md` (the exact bytes the
agent read), `runs/<slug>/agent.log`, and `.review/<slug>/` for each sample
that recorded. A sample that fails keeps its files and its bundle is dropped,
so `trend` scores only what recorded; the failure lands in `failures[]` with
its error and evidence path.

The publication JSON is a checked-in artifact, so run `bun fmt` on it before
committing: the pre-commit hook checks tracked JSON. It carries the corpus
commit and case count, the model and panels, `prompts_hash`, the per-sample
calibrate blocks, the raw `trend --json` `data.corpus` block, the cost rollup
(measured or not), and the per-case median/min/max spread. `citable` is `true`
only for three samples per case, the whole corpus, no failures, and no
`-internal`; otherwise the report names what is missing. Publish it as the
runbook says, or leave it a pilot.
