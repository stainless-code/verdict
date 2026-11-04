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
