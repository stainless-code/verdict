# CI and test rules

`AGENTS.md` carries the verification ladder. This file carries the rules that
ladder cannot hold, each with the gate behind it and the same marker
vocabulary [`go-rules.md`](go-rules.md) uses: `— enforced:` fails the build
today, `— partly:` means the named gate covers part of the rule, `— review-only:`
means a human reviewer is the only gate.

## Commit messages

- **Never a CI-skip directive in a commit message, not even quoted in a body.**
  The family is `[skip ci]`, `[ci skip]`, and `[no ci]`. — review-only: GitHub
  acts on the token before any job starts, and `release.yml` triggers on the
  `main` push alone (`.github/workflows/release.yml`), so a skip on the merged
  version commit drops every gate on that push _and_ the tag it exists to cut.
  Nothing in `ci.yml` can report it: the run never starts. A push that needs no
  CI says so in the PR body.

## Test determinism

- **A test that can time out while the code is right has a race to fix.** Never
  a larger `-timeout`, never a rerun, never a `t.Skip` to quieten the share. A
  build that goes green on retry is a defect report about the test, and the
  next one is a finding nobody trusts. — partly: `make test-race` runs every
  package once under `-race -shuffle=on` (GO-CONC-07), which surfaces the
  order- and interleaving-dependent class; a widened bound still passes.
- **Poll with `testenv.Until(t, cond)`.** One 15 s bound and one failure shape
  for every wait the test goroutine blocks on, so a child that never reaches
  the awaited state fails the test instead of the suite's global timeout. A
  driver goroutine that must act on either outcome keeps its own bounded loop,
  because `t.Fatalf` cannot run there. — review-only: a raw `time.Sleep` poll
  compiles and passes.

## Agent launch (`run --agent`)

- **The launcher is graded end to end, not only by unit tests.**
  `verdict run <repo> <pr|local-name> --agent <name>` spawns one configured
  agent process and asserts exactly one fresh `review.json`. The unit tests
  drive a scripted commander, so nothing there spawns the shipped argv shape
  against a produced bundle. — enforced: `evals/cases/run-agent.sh`
  (`./evals/one.sh run-agent`), and the CI `evals` lanes re-read
  `evals/results/summary.json` to prove that case `PASS`ed rather than
  trusting the suite to keep running it.

**What it runs.** `make build`, then one case: `./evals/one.sh run-agent`
(`make evals` runs it with the rest). The "agent" is the committed stub
`evals/fixtures/run-agent/agent-stub.sh`, copied onto `PATH` as `claude`, so
the launcher resolves a name out of its own table and the shipped argv shape
is what runs. The stub reads the review path out of the launcher's directive
and writes that one `review.json`. The lane needs no model, no secret, and no
network: a stub `gh` answers first on `PATH`, and the single intercepted call
in the whole case is the PR dry-run's head lookup (the case asserts `run`
itself made zero host calls).

**What it proves.** `--print` renders the exact argv and spawns and writes
nothing. A launch spawns exactly one process in the repository root, hands it
the directive after the operator's pass-through flags, and accepts exactly one
fresh `review.json`; `launcher.json` records the agent, the resolved binary,
the redacted argv (`<prompt>` placeholder, `***REDACTED***` secret operand
while the child still received the real value), timings, exit code, and
freshness. `record` merges the launched review byte-identically, and on a PR
bundle `post --dry-run` accepts the same file at `COMMENT` and writes no
`post.json`. Every failure is typed: `agent-missing`, `unknown-agent`,
`bad-timeout`, `missing-bundle`, `no-review-artifact`, `stale-review-artifact`,
`agent-failed` (whose exit code becomes the process exit code), and
`agent-timeout` (the watchdog kill, with `timed_out: true` in the record).

**What it does not prove.** No real agent or model runs here, so nothing in
this case grades review judgment — calibration does. Freshness is a
modification-time comparison at one-second granularity, so the guarantee is
"not older than the launch", never "written by this agent": an artifact left
behind in the same second as the launch record reads as fresh by design.
`run` never posts: with the default one shot it never records, repairs, or retries (with `--rounds N` it repeats only the launch→check pair, up to N rounds, and still records nothing): the dry-run leg proves the
review is postable, not that anything was submitted. `--timeout` is a kill
budget, not a cost or turn bound, and none of this touches the host.

**Locally.** `make build && ./evals/one.sh run-agent` (needs `bash`, `jq`,
and `git`; seven agent launches, a few seconds). `make evals` runs it with
every other case; the runner fails when `bin/verdict` is missing rather than
grading nothing.
