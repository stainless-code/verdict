# TypeScript client examples

Five examples that call the mutating verbs through
`@stainless-code/verdict/client` and run end to end: no network, no mock of the
client's own return value, and no hand-made bundle. Each one builds a throwaway
git checkout, has the real `fetch-local` producer write the bundle, and calls
the shipped client, which spawns the `verdict` binary this checkout built.

## Run them

```sh
make build && make clients-ts           # bin/verdict, then clients/ts/dist
node examples/typescript-client/record.ts
node examples/typescript-client/post-dry-run.ts
node examples/typescript-client/config.ts
node examples/typescript-client/gate.ts
node examples/typescript-client/fix.ts
```

`make clients-ts` also runs `clients/ts/test/examples.test.mjs`, which executes
all five and compares the `ok:` lines they print, so a stale example fails the
TS client gate instead of rotting in the tree.

Node ≥ 22.18 runs the `.ts` files directly (type stripping); this repository
gates them on Node 24. Set `VERDICT_BIN` to use a binary other than the built
one — the examples pin it, so a different `verdict` earlier on `PATH` is never
the one that runs.

## What each example proves

| Example           | Contract it exercises                                                                                                                                                                                                                                                                                                                                            |
| ----------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `record.ts`       | `record --dry-run` runs the whole merge and writes nothing (`data.dry_run`, `data.check`, `data.evidence`); a real `record` writes `findings.json` and `record.json` and reports where; a GitHub PR bundle refuses `not-local`, because `post` owns that target; a malformed review refuses `bad-review-input` naming the broken JSONPath in `error.problems[]`. |
| `post-dry-run.ts` | `post --dry-run` previews one payload — `data.payload_hash`, `data.event`, `data.findings.file` = `findings.dryrun.json` — performs exactly one host call (the live head read behind the stale-head guard), and writes no `post.json`; a local bundle refuses `no-pr-target` before any host call.                                                               |
| `config.ts`       | `config --explain` renders the stored policy with each value's origin; `--preview --file` resolves a proposed `.verdict/config.json` and writes nothing; an invalid proposal refuses `bad-config` naming the JSONPath; asking for neither mode is an exit-2 usage error with no envelope.                                                                        |
| `gate.ts`         | `gate --max-severity <tier> --live skip --coverage warn` runs the composed local verdict over the recorded bundle and stays offline; the important tier fails the open finding while the critical tier passes; asking for no dimension is an exit-2 usage error with no envelope.                                                                                |
| `fix.ts`          | A single `fix` prints the top open inline finding with its KB context and writes the pass ledger; `fix --all --verify-cmd` hands out one `ready` row per open finding and consumes the next pass; a batch without a verify command refuses `no-verify-cmd`.                                                                                                      |

## `record.ts`

```text
record acme/app local-demo --dry-run
  verb                record
  dry_run             true
  check               {"findings":1,"anchors_moved_to_body":0}
  receipts            {"total":1,"covered":1,"exempt":0,"complete":true}
  warnings            []
ok: record --dry-run previews the merge and writes nothing
record acme/app local-demo
  findings            {"open":1,"deferred":0,"addressed":0,"file":".verdict/state/local-demo/findings.json"}
  anchors             {"kept":1,"moved":0}
  next                report or triage the local bundle; post stays refused (no PR target)
ok: record writes findings.json and record.json
record acme/app 4243
  code                not-local
  hint                try: run the fetch/record step named above first, then re-run
ok: record refuses a provider PR bundle with a typed not-local
record acme/app local-demo --dry-run (malformed review)
  code                bad-review-input
  problem path        $.findings[0].path
ok: record refuses a malformed review with typed problems
```

The dry run is the one to reach for in an agent loop: it runs the same
`CheckReview` pass, the same evidence checks, and the same anchor partition a
real record does, and returns `data.normalized` — the exact object the checker
read — plus `data.repairs` when the safe rewrites fired. Nothing on disk moves.

## `post-dry-run.ts`

```text
post acme/app 4243 --dry-run
  verb                post
  dry_run             true
  event               COMMENT
  commit_id           dab932700cbbaf1d8685e8293a6c0764d52c29c2
  findings            {"open":1,"deferred":0,"addressed":0,"file":".verdict/state/4243/findings.dryrun.json"}
  payload_hash        de0f56be60ed807c3b4d694beb4ed6eaabe83681
  next                payload NOT posted (dry-run)
  gh calls            ["api repos/acme/app/pulls/4243 --jq .head.sha"]
ok: post --dry-run previews one payload and posts nothing
post acme/app local-demo --dry-run
  code                no-pr-target
  hint                try: run the fetch/record step named above first, then re-run
ok: post refuses a local bundle with a typed no-pr-target and zero writes
```

`--dry-run` is not fully offline: it still performs the live head read that
backs the stale-head guard, which is why the example installs a stub `gh` as the
first `gh` on the child `PATH` and asserts the call log holds exactly that one
argv. `post.json` — the receipt that proves a submission — is the file the dry
run never writes.

## `config.ts`

```text
config acme/app 4243 --explain
  mode                explain
  trusted             false
  source              run bundle local-demo (base daa2c3aff0073668a85ee7c112c6f4dbcf350816)
  plan_status         pending
  max_bytes           512000
  spec_coverage_min   80
  spend               {"guard_usd":null,"personal_limit_usd":null,"require_known_estimate":false,"enforce_cap":false}
ok: config --explain renders the stored policy and its origins
config acme/app 4243 --preview --file proposals/proposed-config.json
  mode                preview
  plan_status         pending
  spend               {"guard_usd":3,"personal_limit_usd":null,"require_known_estimate":false,"enforce_cap":false}
  budget_usd          3
  budget source       proposed file proposals/proposed-config.json
ok: config --preview resolves a proposed file and writes nothing
config acme/app 4243 --preview --file proposals/bad-config.json
  code                bad-config
  message             $.defaults.budget_usd: want a number above 0, got string
  hint                try: re-run with one of the valid values named above
ok: config --preview refuses a bad proposal with a typed bad-config
config acme/app 4243
  usage               config wants exactly one of --explain or --preview
ok: config without a mode is an exit-2 usage error, not an envelope
```

`--preview` is the validation surface for a proposed `.verdict/config.json`:
it resolves the file through the same strict parser `fetch` uses and returns
the values with their layer and file, so a policy change can be checked before
it lands. It never writes, and it never approves.

## `gate.ts`

```text
gate acme/app local-demo --max-severity important --live skip --coverage warn
  passed              false
  findings            {"id":"findings","status":"fail","detail":"1 open finding(s) at-or-above important","count":1}
  spec                {"id":"spec","status":"pass","detail":"no violated spec check","count":0}
  evidence            {"id":"evidence","status":"pass","detail":"evidence checks satisfied (1 quotes verified, 1/1 hunks receipted, 0/0 spec items checked)","count":0}
  next                address or triage the open findings
ok: gate fails on one open finding at the important tier
gate acme/app local-demo --max-severity critical --live skip --coverage warn
  passed              true
  findings            {"id":"findings","status":"pass","detail":"no open findings at-or-above critical","count":0}
  next                none — every requested dimension passed
ok: gate passes when the floor sits above every open finding
gate acme/app local-demo
  usage               gate needs at least one dimension (want --max-severity critical|important|informational, --require-head, or --max-risk low|medium|high|critical; --live only sets the offline posture)
ok: gate with no dimension is an exit-2 usage error, not an envelope
```

A failing gate is exit 1 with `error` null and the verdict in `data`, so the
client raises `VerdictGateError` carrying the envelope — the example reads the
failing dimension off the error instead of guessing from a message. `--live
skip` makes zero network calls and `--coverage warn` keeps the absent
attestation advisory, so the run is fully offline; a gate with no tier or
`--require-head` is an exit-2 usage error rather than a verdict.

## `fix.ts`

```text
fix acme/app local-demo
  sig                 ecd88617e7f5f9b32a4681a395a813e9cff0f877
  locator             app/handler.go:5
  severity            important
  pass                1
  ledger              .verdict/state/local-demo/fix.json
  next                patch the anchor file, commit it, re-run fetch-local, run verdict verify, write verification.json, run record to land it, then re-run fix
ok: fix prints one pass over the open finding and writes the ledger
fix acme/app local-demo --all --verify-cmd true
  open_inline         1
  batch               [{"sig":"ecd88617e7f5f9b32a4681a395a813e9cff0f877","locator":"app/handler.go:5","severity":"important","pass":2,"outcome":"ready","reason":""}]
  next                patch each anchor, commit the fixes, re-run fetch-local so the bundle head is that commit, run the verify command through verdict verify, write one verification.json claim per sig, land with record, then re-run fix --all
ok: fix --all hands out one ready pass per open finding
fix acme/app local-demo --all
  code                no-verify-cmd
  hint                try: write verifyCmd in the bundle's patch.json (the command verify and fix --all hand back), or pass --verify-cmd <cmd> to a fix batch, then re-run
ok: fix --all without a verify command refuses typed
```

`fix` writes `fix.json` — the pass ledger — and, once the tree is dirty,
`fix.patch` beside it; the fixture tree is clean, so no patch is written. The
single pass consumes pass 1, and the `--all` batch consumes pass 2. Both read
the recorded store, which is why each example records before it fixes.

## Files

| File                            | Purpose                                                                                                      |
| ------------------------------- | ------------------------------------------------------------------------------------------------------------ |
| `lib/sandbox.ts`                | The throwaway checkout, the `fetch-local` bundle, the PR reshape, and the `gh` stub.                         |
| `lib/expect.ts`                 | Literal assertions, the refusal helper, and the printed `ok:` checkpoints the test compares.                 |
| `fixtures/gh`                   | Network-dead `gh`: one exact argv answered, everything else exits 64. Same shape as `evals/lib/hoststub.sh`. |
| `fixtures/proposed-config.json` | A valid proposal for `config --preview`.                                                                     |
| `fixtures/bad-config.json`      | The same proposal with a string where a number belongs.                                                      |
| `tsconfig.json`                 | Typechecks the examples against `clients/ts/dist`; `make clients-ts` runs it.                                |

## How the sandbox is built

1. `git init` a temporary checkout with a pinned author and committer date, so
   every run produces the same SHAs and the output below does not drift.
2. Commit the base file, then the reviewed change, and run
   `verdict fetch-local acme/app demo --range HEAD~1..HEAD`. That is the one
   command that writes `source.json`, `full.diff`, `hunks.tsv`, `controls.json`
   and the run capture `record` and `post` gate on — a hand-written bundle
   cannot, which is why the fixture is produced rather than committed.
3. Copy that bundle to `.verdict/state/4243` with `PR.json` in place of
   `source.json`: a provider PR bundle that `post` and `config` accept and
   `record` refuses.
4. Write `review.json` — the agent's output — quoting the added line verbatim
   and carrying one receipt per changed hunk.
5. Install the `gh` stub, with an empty personal settings layer (`HOME` and
   `XDG_CONFIG_HOME` point into the sandbox) and the `REVIEW_*` knobs emptied,
   so a developer's own environment cannot change the output.

The example that only needs `post` still uses the same sandbox, so every
example reports the same fixture.

## Why `fetch` has no example

`fetch <owner/repo> <pr>` needs a live PR: it reads the provider through
`gh` (and `--preview` does too). The sandbox `gh` stub answers exactly one
argv and refuses everything else, so a fetch example could only pass by
stubbing the PR reads — covering the stub, not the verb. No runnable offline
TypeScript example exists for `fetch`; `fetch-local`, its offline half, is
what produces these bundles. The Go `ExampleClient_Fetch` covers `fetch`
through the scripted commander instead.

The `post` example needs a POSIX shell for `fixtures/gh`; it is graded on
Ubuntu in CI and runs under `sh` on macOS. On Windows, use WSL or run `post`
against a real checkout without the example.
