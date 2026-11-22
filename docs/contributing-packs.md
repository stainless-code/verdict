# Contributing language packs, eval fixtures, and host adapters

Verdict grows in three places, and each has its own bar: **language packs**
(add review rules for a stack), **offline eval fixtures** (pin behavior so it
cannot regress), and **host adapters** (teach a verb to talk to another forge).
This page is the copy-pasteable guide for all three. It assumes you have read
`CONTRIBUTING.md` (the merge bar) and `AGENTS.md` (the house rules for this
checkout).

Start from a clean build so every command below can run the real binary:

```bash
make build             # writes bin/verdict, the binary every command below runs
bin/verdict doctor     # preflight the harness
```

## Language packs

A pack is **data, not code**: a strict-JSON manifest plus Markdown bodies. No
Go is involved in adding a language, which is exactly why a pack is a good
first contribution.

### Where packs live

| Layer   | Location                                                    | Read from                                                                                     |
| ------- | ----------------------------------------------------------- | --------------------------------------------------------------------------------------------- |
| builtin | `procedure/packs/builtin/` and `procedure/packs/languages/` | the binary's embedded payload                                                                 |
| org     | the `packs` array in personal settings                      | the operator's settings file                                                                  |
| repo    | `.verdict/packs/<name>/`                                    | the **PR base commit** for a review (`controls-stale` on drift); the checkout for `pack list` |

The base-commit guarantee covers a **review**: `fetch`/`fetch-local` read repo
packs from the trusted-base snapshot, and `--allow-head-controls` is the one
flag that reads them from the worktree instead (and stamps that choice in
`controls.json`). `verdict pack list` is an inventory verb, so it reads the
packs in **this checkout** — a pack the PR adds or edits is listed there but is
never used by the review it accompanies.

A new curated language pack is a directory under
`procedure/packs/languages/<stack>/`. The shipped set is the eleven stacks
listed in `docs/manual.md` § Lens packs; a twelfth follows the same shape.

### The directory layout

```
procedure/packs/languages/<stack>/
  pack.json          # the manifest (strict JSON, schema_version "1")
  lenses/<id>.md     # one body per lens the manifest declares
  rules/<id>.md      # one body per rule the manifest declares
  evals/<case>/      # offline grading cases (payload, not parsed by the loader)
    diff.patch
    findings.json
    expect.json
```

`examples/` is reserved for illustrative material: the loader carries it
through vendoring and folds it into the tree hash, but parses nothing in it. A
file no manifest row names is never loaded.

### `pack.json`

Model it on a shipped pack — for example `procedure/packs/languages/go/pack.json`
— and validate against the published schema, `schemas/pack.schema.json`.

```json
{
  "schema_version": "1",
  "name": "verdict/<stack>",
  "version": "1.0.0",
  "severity": {
    "critical": "critical",
    "important": "important"
  },
  "lenses": [{ "id": "<stack>", "globs": ["**/*.<ext>"], "max_files": 12 }],
  "rules": [
    {
      "id": "<stack>-<hazard>",
      "severity": "important",
      "globs": ["**/*.<ext>"]
    }
  ]
}
```

Every entry in `rules[]` requires `globs`, with at least one entry: a rule is
recalled only for a file its globs match, so a rule row without globs refuses
`bad-pack` (`validateRules` in `internal/core/pack/pack.go`, and
`schemas/pack.schema.json` carries the same `minItems: 1`). `globs` is optional
only on `lenses[]`, where an absent list offers the lens to every plan group. A
rule that talks about a specific tool may name it in the optional `tools`. Rule
ids follow the KB grammar `^[a-z0-9][a-z0-9-]{0,64}$`, and `severity` is a key
into the pack's own `severity` map whose value is `critical`, `important`, or
`informational`.

Globs decide recall, and nothing else does: a rule whose globs match no changed
file is never selected for that review, and no manifest field can widen that.

Limits are checked while walking, before any parse — 64 KiB per file, 1 MiB per
pack, 256 files, 64 lenses, 8 directory levels (`pack-too-large`), `max_files`
in 1..24, `max_findings` in 1..20. Unknown manifest fields refuse `bad-pack`, an
absolute path or `..` refuses `pack-unsafe-path`, and two packs in one layer may
not declare the same lens or rule id.

### Writing the rule body

The rule body is the reviewer's instruction. Every shipped body is a single
paragraph of positive instruction — `procedure/packs/languages/go/rules/go-error-loss.md`
is the template — and none of them carries a "do not flag" clause. That is not
an accident to copy: state the boundaries anyway. Prose in a rule body is not a
gate — `pack test` grades selection and citation coverage, and a judgement-level
false positive is graded by measurement (see the eval case below) — so a body
that names only the hazard leaves the safe pattern beside it to the reviewer's
decisions:

```markdown
Report <the concrete failure> when <the trigger criteria>. A <the deliberate
pattern that looks like the hazard>, a <the idiom that handles it correctly>,
and code outside the changed hunks are not findings.
```

Name the failure concretely ("an error discarded with `_`", not "error
handling"), and prefer the pattern a reviewer can point at over the property
they have to reason about.

### Writing the lens body

A lens is one falsifiable question about the changed files, with its scope
stated. `procedure/packs/languages/go/lenses/go.md` is the template, and it
holds the discipline that does not belong in a rule body: which files the
dispatch names, that the on-disk diff is authoritative, `max_files` scoping
(~12 files there), **NEW** line-number citations, and the instruction never to
write "review the code" but to name the concrete failure.

### The eval case: what `pack test` actually grades

Every curated pack ships at least one case so its rules are graded offline.
`verdict pack test` runs no model: `selectRules` matches each manifest rule's
`globs` against the file paths in `diff.patch`, and the expectation and findings
are then checked against that selection (`internal/packtest/test.go`). Three
files per case:

| File            | What it must contain                                                                                                                                      |
| --------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `diff.patch`    | a small unified diff whose file paths decide which rules are selected                                                                                     |
| `findings.json` | a JSON **array** of findings — not an object wrapper — carrying the fields the shipped cases use (`path`, `line`, `body`, `rigor`) and a non-empty `kb[]` |
| `expect.json`   | `{"rules": [...]}`, required, plus optional `"not_rules": [...]`                                                                                          |

`rules[]` is a two-part demand: each id must be selected for the diff's paths
**and** cited by at least one finding's `kb[]`. An id with no citation fails as
`missing finding coverage for rule <id>`, and a citation of an id not selected
for that finding's own path fails as `cites unselected rule <id> for <path>`.

`not_rules[]` is a **path-scope assertion**, not a false-positive trap. It fails
as `not_rules hit: <id>` when the rule's globs match any path in the diff, or
when a finding cites the id. It proves a rule's globs do not reach a path — a
`**/*Test.kt` rule staying out of a `src/Foo.kt` change, a narrow glob not
over-matching — and it cannot express "same globs, but this change is safe":
a rule whose globs match the diff is always selected, so listing it under
`not_rules[]` fails the case.

What that means for a case you write:

- Assert the rules the diff selects, and cite each of them in `findings.json`.
- Use `not_rules[]` only for a rule whose globs genuinely do not match this
  diff's paths.
- For a "nothing to report here" fixture, use `{"rules": []}` with
  `findings.json` set to `[]`. That grades that the fixture parses and that no
  `not_rules[]` entry was selected — it does not grade that a model stayed quiet.
- Judgment-level false positives, the safe pattern sitting right beside the
  hazard, are **not** graded automatically by `pack test` or by
  `verdict backtest`: `pack test` runs no model, and `backtest fixtures` scores
  the eight shipped `bench/quality` recordings rather than your pack case. Test
  that fixture yourself — run a review with a real agent against it, or add the
  change to the bench corpus — and record the result by hand.

`expect.json` must carry `rules`, and an empty array is valid; `not_rules` is
optional but must be an array when present. A case that omits one of the three
files, or whose selected rules name a tool that is not installed, is
**skipped**, and any skip or failure makes `pack test` exit 1. A skip is never a
pass.

### Verify a pack

```bash
# grade the pack's own eval cases; --dir points at an unpacked pack tree
bin/verdict pack test <name>
bin/verdict pack test --dir procedure/packs/languages/<stack>
```

A builtin pack is compiled into the binary — `skillfs` embeds `procedure/` — so
`pack test verdict/<name>` grades the **embedded** copy. Iterate with `--dir`
against the working tree, then `make build` and re-run the case by name before
you open the PR, or you will grade the pack you shipped yesterday.

The rest of the contract reads the tree directly:

```bash
# the shipped-pack contract: every curated pack passes its own cases
go test ./internal/packtest/...

# the pack-specific eval cases, run against the built binary
./evals/one.sh pack-test
./evals/one.sh pack-check
./evals/one.sh pack-layers

# manifest and schema drift
go test ./internal/core/pack/...
make schemas-check
```

Checklist before opening the PR:

- [ ] `pack.json` validates against `schemas/pack.schema.json`; no unknown fields, and every rule row carries at least one glob
- [ ] every lens and rule id has its `<id>.md` body, and every body is non-empty
- [ ] each rule body names the failure and the patterns it must not flag
- [ ] at least one eval case whose `rules[]` ids are also cited in `findings.json` `kb[]`
- [ ] any `not_rules[]` names only a rule whose globs do not match that case diff
- [ ] `bin/verdict pack test <name>` passes with zero skips (after `make build`, so it grades the embedded copy)
- [ ] `go test ./internal/packtest/...` passes
- [ ] a new curated stack is registered in three places: the `12` count in `TestShippedPacksPassTheirOwnCases`, the `want` list in `TestShippedLanguagePacksCarryALensAndARule` (`internal/packtest/shipped_test.go`), and `evals/cases/pack-layers.sh` (the `12` builtin count, `14` total, and the org/repo row indices that follow)
- [ ] `docs/manual.md` § Lens packs updated only if the layout or limits move
- [ ] a changeset if the pack ships to users (`.changeset/<name>.md`)

## Offline eval fixtures

The eval suite is the contract for the CLI's behavior. It runs with no network,
no model, and no host, and it must pass on a laptop and on CI identically.

### Where a case lives

- `evals/cases/<name>.sh` — the case itself (sourced helpers from
  `evals/lib/common.sh`); `evals/run.sh` picks up every file there, so there is
  no registration step.
- `evals/fixtures/<name>/` — its input files.
- `evals/README.md` — one row per case in the case table. The `doc-coverage`
  case fails when a case has no row, when a row names a case that is gone, or
  when a case is documented twice.

### Rules a case must follow

- **Network-dead by construction.** Source `evals/lib/hoststub.sh`, then
  `hoststub_init <tool>`, `hoststub_prove_dead <tool>`, and
  `hoststub_route <tool> '<argv>' <fixture>`. A stub answers only the argv the
  case registered and refuses anything else with exit 64, so a case cannot pass
  against a call nobody registered. Proxy variables are pointed at a dead port.
- **No `/tmp`.** Use the case's own sandbox directory; for Go, `t.TempDir()` and
  `internal/testenv`. A hardcoded path leaks across parallel runs.
- **Never a real `post`.** `post --dry-run` plus a stubbed host client is the
  only accepted shape.
- **Pass, fail, and skip are three different things.** A leg that could not run
  calls `skip` / `skip_case` and the run fails unless `EVAL_ALLOW_SKIP=1`; an
  absence is asserted with `assert_file_nogrep` / `assert_stream_nogrep`, never
  a bare `! grep -q`.

### Verify a fixture

```bash
make build
./evals/one.sh <case>       # one case while iterating
make evals                  # the full hermetic suite before the PR
```

Checklist:

- [ ] the case fails when the behavior regresses (mutate the code and watch it
      go red once)
- [ ] every host CLI call goes through `hoststub.sh`
- [ ] a row for the case in `evals/README.md`, in the documented columns
- [ ] `./evals/one.sh <case>` passes; `make evals` passes
- [ ] `shellcheck -S warning evals/cases/<name>.sh` is clean

## Host adapters

Teaching `fetch`/`post` another forge means adding an adapter, not a verb. The
layout is deliberately one file per host on each half:

| Path                                  | What it is                                                           |
| ------------------------------------- | -------------------------------------------------------------------- |
| `internal/core/provider.go`           | provider inference from the origin URL and the explicit `--provider` |
| `internal/core/fetch/capabilities.go` | what each host supports (post, incremental, approvals)               |
| `internal/core/post/host.go`          | the `hostWriter` / `hostReader` interfaces an adapter satisfies      |
| `internal/core/post/host_github.go`   | GitHub adapter                                                       |
| `internal/core/post/host_gitlab.go`   | GitLab adapter                                                       |
| `internal/core/post/host_forgejo.go`  | Forgejo adapter                                                      |
| `internal/core/ready/host_gitlab.go`  | GitLab readiness reads                                               |
| `internal/forgejo/forgejo.go`         | the Forgejo REST calls the adapter shares                            |
| `internal/curlauth/`                  | curl credential handling for the REST-based hosts                    |

Read `docs/manual.md` § Providers (non-GitHub hosts) first: a host that cannot
do something must **withhold** it and record that in `withheld[]`, never fake
it. A GitLab post, for example, has no `REQUEST_CHANGES` review event, so a
binding run posts its discussions and summary as a comment and records
`request_changes` / `no-request-changes-event`.

Every adapter is graded offline, with the host CLI stubbed network-dead:

```bash
./evals/one.sh gitlab-fetch-post
./evals/one.sh forgejo-post
./evals/one.sh forgejo-fetch
./evals/one.sh bitbucket-fetch
go test ./internal/core/post/...
```

Checklist:

- [ ] provider inference and the capability matrix both know the new host
- [ ] every refusal is typed and carries a `try:` hint
- [ ] a live read you cannot perform is withheld and named, not silently skipped
- [ ] at least one hermetic eval case with `hoststub.sh` routes
- [ ] `docs/manual.md` § Providers and the `README.md` host table updated
- [ ] no network in any test; `post --dry-run` only

## Opening the pull request

Use the matching issue template — `language_pack.yml` for a pack,
`eval_fixture.yml` for a fixture — and fill in the PR template checklist. The
merge bar in `CONTRIBUTING.md` applies in full: `make evals`, `make test-race`,
`golangci-lint run ./...`, `bun fmt --check`, and a changeset when the change
reaches users.
