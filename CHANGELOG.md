# @stainless-code/verdict

## 0.7.0

### Minor Changes

- 1f17fba: Mine SZZ candidates from a local clone. `backtest mine --since
<90d|12w|18mo|2y|ref> [--until <ref>] [--run <slug>]` blames each later
  `fix:`/`Revert "..."` commit's deleted lines to the merged or squash PR that
  introduced them and writes
  `.verdict/backtest/<run>/{run.json,mine.json}` with zero model calls and zero
  network. `--run` ids are at most 42 bytes, the room a replay bundle name has
  for one, so a mined run is always replayable. Candidates carry the PR head, base, reconstruction, merge/head dates,
  and the union of anchored `path:line:kind` bugs; exclusions are counted by
  reason and never refused. `mine.json` is timestamp-free, so a rerun over the
  same window is byte-identical. The artifact is not citable: SZZ blame is a
  hypothesis. Replay and score read it. The verb is CLI-only because replay
  spends on model runs.
- d6582b6: Replay mined candidates against your own agent. `backtest replay --run <id>
--agent <name> [--max-reviews <n>] [--budget-usd <usd>] [--timeout <secs>]
[--rounds <n>] [-- <agent args>]` walks the run's `mine.json` newest merge
  first, builds a fresh local bundle at each candidate's own `base..head`,
  launches the agent in a detached `git worktree` of that head (removed again
  after each review, failures included), and records the review into the
  bundle's `findings.json`. Spend is bounded by `--max-reviews` (1–99), a
  median-projected `--budget-usd` ceiling, and a per-review `--timeout`; a review
  that reported no price is unknown spend, never `$0.00`. A failed review is
  that candidate's row (`status` is the refusal code) and the walk continues;
  only `agent-missing`, `agent-spawn-failed`, cancellation, and a
  `backtest-failed` that would hit every candidate (git gone mid-run, or a
  state directory that cannot be created) end the run.
  `--run` ids are capped at 42 bytes by `backtest mine` and `backtest replay`
  alike, so two runs can never be cut onto one bundle name.
  Rerunning the same `--run` replays only candidates that produced no review, and
  a stored candidate replayed with another agent or other pass-through arguments
  refuses `run-mismatch`. Every bundle carries a `backtest.json` marker, so
  `trend` keeps replay input out of your history. Artifacts:
  `.verdict/backtest/<run>/prs/<NNN>/replay.json`. Replays are not citable.
- 3b2b92c: Score a replay, and run the whole backtest in one command. `backtest score --run
<id>` grades each replayed PR's recorded findings against the bug lines the
  run's own SZZ candidates carry, through the corpus matcher (±1 line, one
  finding per bug, text that names the defect kind), and writes
  `.verdict/backtest/<run>/report.json`. It is a pure read: no agent launches, no
  bundle is built, and nothing enters `.verdict/state/corpus-*`.
  `catch_rate` is `matched / bugs`, a match rate over blame-derived candidates —
  a line a later fix deleted is a hypothesis, so a defect fixed without touching
  the line is invisible and a deleted cleanup line beside the real bug is a
  candidate that was never defective. `off_target_per_pr` is `(reported −
matched) / prs`; an unmatched finding is not proven false, so the report never
  calls it one.
  `cost_per_review_usd` is null when nothing was priced, and
  `spend.unpriced_reviews` marks a partial total as a lower bound. A per-signal
  table splits the rate by `fix` and `revert`, `unclassified` counts bugs no
  finding can name, and every zero denominator renders `null`. `backtest mine`
  now records each bug's seed `signal` in `mine.json`. Bare `backtest --since
<window> --agent <name>` composes mine, replay, and score on one run id. The
  score report is also the verb's `--json` data, published as
  `schemas/backtest.schema.json`. Scores are not citable.
- 73cba6b: Accept clean benchmark controls with empty ground truth and seeded or real
  provenance. Include two seeded clean cases and exercise clean review recording
  and calibration through the CLI. Seeded fixtures are not a model-quality claim.
- 751d7fd: Make corpus benchmark claims reproducible with typed severity weights,
  ordered k-of-n recall, control false-positive rates, weighted F1, signal to
  noise, and explicit denominators and observation counts. Preserve unknown
  cost and latency as null. The benchmark runner records provenance and command
  argv and writes a static Markdown report alongside JSON. Seeded runs are
  labelled "not a model-quality claim".

  Metric names distinguish false discovery rate from control-PR false-positive
  rate. `control_fp_rate` counts controls with any finding; raw counts use
  `control_findings_per_case`. Weighted F1 is named
  `severity_weighted_pooled_f1` and its pooled formula is documented.

- 0504186: Add bounded foreground GitHub PR waits through `pr-wait`, MCP `pr_wait`, and
  Go and TypeScript clients. Conditions cover merge, closure, checks and review
  changes. Timeouts return typed data. `ready --wait-for build --timeout D` waits
  for CI completion and returns not-ready on timeout. GitLab and Forgejo waits
  refuse explicitly; no daemon or saved cursor is introduced.
- fe2a53c: Refuse a bundle name that is not one path element. Every verb that names a
  bundle — adopt, calibrate, config explain/preview, fetch, fix, gate, index
  blast-radius, next, post, ready, record, report, run, show, threads, triage,
  verify, and the MCP tools that take a `pr` — resolves the name through one
  shared guard before it joins `.verdict/state`, so `../other`, `a/b`, `a\b`,
  `.`, `..`, and the empty name refuse `bad-flags` with one line instead of
  reading outside the bundle home. The same guard refuses, on every OS, the
  spellings Windows re-reads as another path or as a device: any `:`, a trailing
  `.` or space, and a DOS device basename (`CON`, `PRN`, `AUX`, `NUL`, `COM1`-
  `COM9`, `LPT1`-`LPT9`). `index blast-radius` reported the same input under its
  own message and now renders the shared one. A PR number, a `local-` slug, and a
  `corpus-` name still resolve as before.

  The MCP `next` tool answers a findings store it cannot read with the typed
  `corrupt-store` refusal instead of `{"item": null}`, so a caller no longer
  reads a broken store as "nothing to act on".

  `fetch` and `fetch-local` refuse a bundle directory that is a symlink, with the
  `bad-config` refusal, before the first write: a link's target is an unrelated
  location even when it resolves inside the repository. `trend` reads each listed
  bundle directory it finds instead of resolving the listed name through the
  bare-name fallback, so a `42` beside a `local-42` is no longer counted twice, and
  `42` keeps its own findings.

- 7e03259: Attest diff coverage. `review.json` gains an optional `coverage[]` of
  `{file, ranges?}`, checked against the bundle's own `hunks.tsv`: `post` and
  `record` warn once and add one body line on a partial attestation, and the new
  `--require-coverage` flag refuses `coverage-incomplete` without bypassing
  `--force`. `next --coverage` reports the unattested ranges before recording, and
  `post.json`/`record.json` carry the measured summary. Generated and vendored
  paths are exempt; chunked bundles keep `chunks[]` as their only attestation.
  Legacy reviews render byte-identically.
- 650ce7f: `doctor --provider github|gitlab|gitcafe|forgejo|bitbucket|azure` preflights one
  host instead of always assuming GitHub: the same enum and `bad-provider`
  refusal as `fetch --provider`, defaulting to `github` so a bare `doctor` keeps
  its rows byte for byte. Each host has its own probe table — `gh` and its scope
  rows for GitHub, `glab` presence, `glab auth status`, and a `glab api user`
  reachability read for GitLab, the `cafe` CLI for gitcafe, and `curl`/`az` plus
  the credential variables their fetch paths read (`FORGEJO_TOKEN`,
  `BITBUCKET_TOKEN`, `AZURE_DEVOPS_EXT_PAT`, …) for the rest, reported by presence
  and never by value. A non-GitHub preflight shows no `gh` row.

  `adopt` now hands local findings to any bundle verdict can post to, so a GitLab
  bundle adopts exactly like a GitHub one; its re-anchor reads the stored hunks
  and shells no host CLI. Bundles on a host with no submit path still refuse
  `provider-unimplemented`, with the host named in the message.

- 69ef6fd: Ship the index hand-off. `fetch` and `fetch-local` take
  `--index <ref|dir>`: the operator names an index they already built — a
  revision in the reviewed repository, or a directory holding `.verdict/index` —
  and the bundle gains an `impact` artifact and a `## impact` section on
  `toc.txt`, inside the existing nonce fence. Nothing is built, refreshed, or
  discovered: without the flag no section is written and an earlier run's
  `impact.json` is removed.

  `impact.json` (named in `data.artifacts`) holds the answered tree and commit,
  `stale`, `coverage`, the diff's changed `paths`, the row `limit`, and one
  measured object per list — `symbols`, `callers` (with `unresolved`), and
  `tests` (with `depth_truncated`) — each carrying `count`, `truncated`, and
  `rows`; a list the index could not answer reads `null`, never `0`. `sources`
  carries `index:<tree>`, naming the tree that answered. The `toc.txt` section is
  capped at 8 KiB and ends with `truncated: <n> line(s) omitted to fit the 8 KiB
impact cap` when rows do not fit, and every path and symbol it renders is
  escaped through the shared fence rule, so a hostile name can neither close the
  fence nor start a line of its own.

  A directory with no index store refuses `bad-flags`, an unresolvable ref
  `bad-at`, a tree the store never built `missing-index`, and a truncated tree
  file `index-corrupt`; a named index answering for another tree, and a bundle
  head this clone cannot see, warn visibly and proceed. The four hashed prompt
  files are untouched, so `prompts_hash` does not move.

- 2642839: `post` publishes to Forgejo pull requests over the REST API: one atomic review
  carrying the body, the verdict, the reviewed commit, and every inline finding,
  with `APPROVED` for an approval. A review comment anchors on a file line
  number (`new_position` on the new side, `old_position` on the old), so a
  finding with no numeric line is withheld, a LEFT anchor on a file the change
  deletes is withheld, and a multi-line range lands on its end line; each loss is
  counted in the envelope `warnings`. A 200 that answers `PENDING` refuses
  `post-failed` instead of recording a receipt for a draft nobody sees, and a
  pre-existing draft authored by the token user refuses with a submit-or-discard
  hint. `data.capabilities` now reports Forgejo's post, inline anchors, and both
  binding verdicts.
- f0559c6: Gate on diff coverage by default. `verdict gate` gains a `coverage` dimension
  that reads the receipt `post.json`/`record.json` already store for the gated
  HEAD and fails when that receipt is absent, names another HEAD, carries no
  `coverage[]` attestation (a review that never attested coverage reads
  `unknown`, not `pass`), or attests under 100% — the reviewed ranges are not
  re-evaluated, so the gate reports the number the store holds. `--coverage
require|warn` sets the posture and defaults to `require`; `--coverage warn`
  reports the same shortfall as one `warn:` line and never fails the gate: it is
  how to keep the old behavior and the migration while a review attests nothing. An all-exempt
  diff passes once a receipt exists, a chunked bundle is graded on the chunk
  attestation its receipt stores, and `--coverage` alone is still a usage error.
  `post` and `record` keep the warning posture (`--require-coverage` opts into
  the refusal), the SDK's `GateRequest` gains the same `Coverage` field with the
  same default, and the published envelope schema adds `coverage` to the gate
  dimension ids.

  A chunked bundle is graded too, because `chunks[]` is its only attestation and
  nothing stored that fact. `post.json` and `record.json` gain
  `chunk_coverage: {attested, total}`, written beside the existing measured
  summary at the point the merge already validates `chunks[]`: the manifest is
  the denominator, and only a claim the manifest can name counts toward the
  numerator, so the receipt can never attest more than the review wrote. Every
  chunk attested passes; a shortfall fails naming the count (`1 of 2 chunk(s)
unattested`); a receipt written by an older binary carries no such key and
  still reads `unknown` under `require`. Non-chunked receipts are unchanged byte
  for byte.

- 8199d23: Package the review and merge gate as a first-party composite GitHub Action.
  The runner is your CI — pass your own agent and keys, install a pinned verified
  verdict release, and reuse the pipeline through the shipped example workflow.
- 37d8164: Incremental re-review on GitLab. `fetch --provider gitlab --base-sha <sha>`
  (or `--since`) calls `glab api
projects/<id>/repository/compare?from=<base>&to=<head>`, joins the response's
  per-file `diff` entries into `incremental.diff`, and names the base in
  `incremental_base` — the same artifact, note, and `.compare.err` contract the
  GitHub compare path already writes. A failed or unreadable compare still
  finishes the fetch and writes `incremental.diff.note` instead of refusing.
  Hosts without a compare adapter (GitCafe, Forgejo, Bitbucket, Azure) now accept
  `--base-sha`/`--since` and write the note naming the unsupported provider
  rather than refusing the fetch. The delta feeds the ordinary record/advance
  path, so a second review round reopens delta-touched findings and records at
  the fetched head.
- 7e03259: `post` publishes to GitLab merge requests: each finding becomes one positioned
  discussion, the review body becomes a summary note, and an approval verdict
  becomes a real MR approval. `capabilities` reports GitLab's approval and
  request-changes support separately, and a request-changes verdict on GitLab
  warns that the host has no such event instead of silently pretending.
- 7e03259: Make `data.capabilities` honest. The binding verdict splits into `approve` and
  `request_changes`, because hosts differ: GitHub carries both, while GitLab
  approves a merge request but has no request-changes review event — a bound
  request-changes there still posts the discussions and summary note, and says in
  a warning that the verdict did not bind. `review_verdicts` stays in place as
  the compatibility sum `approve && request_changes`, so a consumer written before
  the split reads the same truth; new readers branch on the two flags.
  `thread_resolution` reads true on GitHub and GitLab, where `threads resolve`
  closes a verdict-authored thread, and false on Forgejo, which has no documented
  resolve endpoint, and on the fetch-only hosts. The new `unsupported` object
  carries one machine-readable
  reason code per absent capability (`fetch-only`, `no-request-changes-event`,
  `no-approve-verdict`, `no-inline-anchors`, `no-resolve-mutation`,
  `unknown-host`). The published envelope schema types `capabilities` instead of
  leaving it open; only `provider` and `fetch` stay required, so an envelope
  written before the split still validates while every new fetch emits the full
  object.
- 0bffc0a: Add repeatable fetch ignore overrides and a switch to disable built-in exclusions, recording both in exclusion receipts.
- e2a47aa: Record receipts for excluded diff bytes and suppressed findings. Add post severity floors and show quiet findings in reports and trend counts.
- 878fadc: Add `index blast-radius --pr <n>|--paths <path>` — one change's reach. The
  changed declarations come from the fetched bundle's `full.diff` (no host call)
  or from named files; their callers are walked to depth 2, once per target and
  cycle-safe; and candidate tests, co-change partners, and CODEOWNERS owners ride
  along for the same paths. A diff section with no line hunks — a rename or a
  mode change — reads as every declaration in its new path and warns that it did.
  A `++` or `--` line inside a hunk is a content line, so its changed line and the
  hunk's anchors stay put. A depth-2 caller names the changed declaration it is
  reached from, not the depth-1 caller in between. A `--pr` that holds a path
  separator or is `.` or `..` refuses as `bad-flags`, since `--pr` names a bundle.
  Every list publishes its count and truncation count, `co_changed` publishes its
  distinct partner count beside the row count, and an absent index reads the
  symbol, caller, and test counts as `null`, never 0. The read-only MCP `index`
  tool serves the new query in both its `--paths` and `--pr` forms, and the
  generated envelope schema types its output for the triage risk score.
- 66ce064: Add `verdict index`: a local Go code index built with the standard library
  (`go/parser`, `go/ast`) and stored under `.verdict/index/` by tree SHA.
  `index build [--from <dir>] [--at <ref>]` writes one tree file plus
  content-addressed shards, so an empty commit reuses the tree, a pure rename
  parses nothing, and an edit parses one blob. `index status` reports the stored
  trees and shards, `index prune` keeps the newest four trees and the shards they
  reference, and `index callers <symbol> [--at <tree>] [--limit <n>]` returns
  call sites labelled `exact` within a package or `lexical` across an import
  edge; method values, cross-package value selectors that may be fields, dynamic dispatch, reflection, and generic instantiation
  are counted as unresolved, never guessed. A package qualifier that matches no
  package directory or import path refuses `bad-flags` rather than reading as no
  callers. Caps
  refuse `index-capped` with the measured number, a truncated shard refuses
  `index-corrupt`, and a query against a tree that is not HEAD's warns and sets
  `stale: true`. The MCP `index` tool is read-only and serves `callers` and
  `status`; the SDK forwards every subcommand. `.verdict/index/` joins the
  tracked ignore contract.
- 45fda1e: Extend `verdict index` with three graph queries. `index tests-for
<path>|--symbol <pkg.Name> [--at <tree>] [--limit <n>]` lists candidate tests
  labelled `same-package`, `references`, or `imports-module` — tests worth
  reading, never coverage claims; only `_test.go` files appear. `index co-changed
<path> [--commits <n>] [--since <dur|ref>] [--min-count <n>] [--limit <n>]`
  reads one bounded `git log --no-merges` window (merges excluded, renames as two
  paths), reports partner paths with co-change counts and four-decimal Jaccard
  ratios, echoes the window it read, warns on a shallow clone, and caches the
  window under `.verdict/index/cochange/` (two newest, timestamp-free bytes).
  `index owners <path>` answers the CODEOWNERS rule at the tree — GitHub's file
  order, last matching rule wins, the documented glob subset only — with
  `owners: null` when nothing matches instead of an owner named nobody. The same
  `target` object (path, owners, source) rides on `tests-for` and `co-changed`.
  The read-only MCP `index` tool serves the three new queries and publishes their
  query enum; the SDK forwards them.
- f86f3bc: Scan TypeScript, JavaScript, and Python in `verdict index`. A build now reads
  `.ts`, `.tsx`, `.mts`, `.cts`, `.js`, `.jsx`, `.mjs`, `.cjs`, `.py`, and
  `.pyi` alongside Go, so `index callers`, `index tests-for`, and `index status`
  answer for those files too. Go stays parsed with `go/parser`; the other three
  are read by two dependency-free lexical scanners — no tree-sitter, no cgo, no
  third-party module — that tokenize the file and record declarations, imports,
  and call sites. Every scanned file publishes its `language`, a build publishes
  `data.languages` with one row per scanned language and how many of the indexed
  files it holds, and a caller row names the language it came from.

  A scanned language resolves one way only: `exact` never appears, and a name is
  attributed as `lexical` inside the directory that declares it, so a caller list
  for a scanned symbol is a floor rather than a proof. Languages never cross: a
  caller row, and a test candidate — however it was found — must share the
  declaration's language family, so Go, Python, and the JavaScript family each
  answer only for themselves.

  Test candidates follow the language's own naming — `.test.` and `.spec.` names
  and `__tests__` directories for the JavaScript family, `test_*.py`,
  `*_test.py`, `conftest.py`, and `tests`/`test` directories for Python — and a
  test in another directory is a candidate only when it imports the queried file
  by a specifier that resolves to it, `from lib import mod` included. A blob two
  languages both claim is parsed per file and cached under neither, so a shared
  file never borrows another language's parse and every build counts those parses
  as parses rather than reuse.

  A file over the 1 MiB cap stays in the tree with `oversize: true` and counts as
  skipped rather than indexed; an extension the index does not scan never enters
  the tree, so it is not counted at all. A store written by an earlier build
  (store version 2 now) reads as unbuilt in `index status` and is rebuilt once by
  the next `index build`.

- 42aad7e: Reach candidate tests through the import closure. `index tests-for
<path>|--symbol <pkg.Name> [--at <tree>] [--depth <n>] [--limit <n>]` walks the
  reverse import graph from the declaring package — breadth first, cycle safe,
  `--depth` hops (default 3, maximum 8; 1 limits the walk to direct imports) — and
  labels tests reached through their package's imports as `transitive-import`,
  alongside `same-package`, `references`, and `imports-module`. Each row carries
  the import hops its own imports took, or its package's when its own imports reach
  nothing, so a sibling test that imports the target directly cannot hide it — and
  a direct candidate at depth 1 never reads as a transitive one. `depth_truncated` counts
  the candidates whose own chain is one hop past the cap. The walk uses each
  language's own import data (Go import paths, TypeScript
  and JavaScript specifiers, Python modules and the submodule a `from … import …`
  names) and never crosses a language family. `index blast-radius` reuses it, so
  its candidate-test list gains transitive rows and a `tests.depth_truncated`
  count of the candidates its fixed depth cap hid; the read-only MCP `index` tool
  takes `depth` too.
- c5a33b5: `verdict ingest <owner/repo> <pr> --sarif <file|->` ingests SARIF 2.1.0 results
  into the bundle's `findings.json` store with `source: "tool"`, sharing one
  store, one dedupe path (`verdict/sig`, `sig`, and `(tool, fingerprint)`), and one
  suppression/withheld path with model reviews. Severities map through
  `skills/verdict/schemas/sarif-map.json`, out-of-diff results stay
  `anchored: false`, excluded paths land withheld (`source: "exclusion"`), and tool
  findings keep `rigor: "said"` (`rigor_source: "default"`).

  `report` and `trend` publish `by_source` (`model` and `tool`) breakdowns,
  `ingest` joins the CLI, MCP default tool registry (`mutates: localWrites`), Go
  SDK (`pkg/verdict`), TypeScript client (`clients/ts`), and published JSON
  Schemas.

- 4a0805e: Propose inactive one-rule packs from normalized resolved review threads, accept
  them through the existing pack importer, or reject them permanently with a human
  reason. All three lifecycle modes support a write-free dry-run. Accepted rules
  enter scoped review recall; quoted thread evidence never activates itself.
- 5205da7: Ship the built-in lenses as a pack. The three lenses `prompts/review.md` step 4
  names today — `perf`, `api-contract`, and `test` — now live as data in
  `skills/verdict/packs/builtin/`: a strict JSON `pack.json` (published as
  `schemas/pack.schema.json`) plus one markdown body per lens. `fetch` and
  `fetch-local` resolve the embedded pack into every bundle as `lenses.txt`
  (each body delimited and labelled with its id and eight-hex hash), list it in
  `data.artifacts`, and append `lens_pack=<name>@<version>` and
  `lenses=[<id>:<hash8>,…]` to each `plan.txt` group line. A lens whose globs
  match none of a group's files is not offered for that group. The loader is
  capped (64 KiB/file, 1 MiB/pack, 256 files, 64 lenses), refuses `bad-pack`,
  `pack-too-large`, and `pack-unsafe-path` (absolute paths, `..`, symlinks), and
  hashes the canonical tree. The four hashed prompt files are byte-identical, so
  `prompts_hash` does not move.
- cee9aca: `verdict mcp config` now covers six more hosts and the server's posture flags.
  `--read-only` or `--operator` (mutually exclusive, like `verdict mcp`) puts the
  matching argv after `mcp` in the
  printed, `--json`, and `--write` snippets. A `verdict` entry whose argv is one
  of verdict's own shapes (`["mcp"]` plus any combination of the two flags) is
  updated in place on `--write` with the usual backup; any other argv still
  refuses typed (`mcp-config-foreign`).

  New JSON `mcpServers` hosts merge with `--write`: `gemini`
  (`~/.gemini/settings.json`), `qwen` (`~/.qwen/settings.json`), and `windsurf`
  (`~/.codeium/windsurf/mcp_config.json`). `opencode` (`opencode.json`),
  `copilot` (`.vscode/mcp.json`), and `goose` (`~/.config/goose/config.yaml`)
  are print-only: the snippet renders each host's own shape and `--write`
  refuses typed naming the host.

- 1b36d07: Declare every MCP tool's local mutation class explicitly and derive read-only
  exposure from it. Calls hidden by `--read-only` now return the typed
  `read-only-tool` tool refusal with a remediation hint.
- 3fbf1a4: Layer lens packs and list them. `verdict pack list [--json]` reports every
  loaded pack (built-in, org from the personal settings `packs` array, repo from
  `.verdict/packs/<name>/`) with its layer, source, canonical tree hash, lens
  and rule ids, and drift (null until `verdict.lock`). Repo packs are read from
  the trusted-base controls snapshot, so a PR cannot edit the pack that reviews
  it; `post`/`record` re-hash the snapshot and refuse `controls-stale`. Duplicate
  ids in one layer refuse `bad-pack`; a cross-layer override needs
  `"replace": ["<lower pack name>"]` (downward only) and otherwise refuses
  `pack-conflict`. A layered set tags each lens in a `plan.txt` group line with
  the full name of the pack that supplied it (`lenses=[<id>@<name>:<hash8>,…]`),
  so two packs sharing a lens id stay distinguishable on the line. A settings
  `packs` entry inside `.verdict/packs/` refuses
  `bad-pack`, because that tree is the repo layer read at the PR base. Packs may
  ship KB rules (`rules/<id>.md`, severity mapped through the manifest) that
  join the fenced `kb.txt` context, never the trusted prompt. Manifest keys must
  be exact lowercase, non-regular pack files and a symlinked pack root refuse
  `pack-unsafe-path`, and body reads are bounded. A regular file directly under
  `.verdict/packs/` is not a pack: `pack list` warns once and `fetch` ignores
  it. A symlink or special file under `.verdict/packs/`, or a `.verdict/packs`
  that is not a real directory, refuses naming the joined path — `pack list`
  as `pack-unsafe-path`, `fetch` as `controls-unavailable` — so neither read
  calls one ignorable. The read-only `pack` MCP tool, `Client.Pack`/`ListPacks`,
  and the TypeScript `pack` method serve the same list.
- 0131d0c: Add `verdict pack test` to run offline lens-pack eval cases.
- bdaf963: Add `verdict pack add <dir|git-url>[@ref]`, which validates a lens pack with
  the existing loader, vendors its tree under `.verdict/packs/<slug>/`
  (including the `evals/` and `examples/` payloads), and pins it in
  `.verdict/verdict.lock`. The lock's tree hash covers those payload trees too,
  so two packs that differ only in an eval case record different hashes. The lock is strict JSON with one row
  per repo pack, sorted by name: `name`, `source` (`dir:<path>` or
  `git:<url>@<sha>`), `version`, the canonical tree `hash`, and an integrity
  `sig` over those fields. A `dir:` source is a slash path with no control
  character, backslash, or leading `-`; neither spelling carries a userinfo
  password. Re-adding a name replaces its tree and entry and
  prints the old and new hash; a source inside `.verdict/packs/` refuses
  `bad-pack`. Git sources fetch hermetically (no system config, scrubbed
  `HOME`/`XDG_*`, no prompts, no hooks, file protocol disabled, shallow clone
  without tags or submodules) and record the resolved HEAD commit; the git calls
  share a five-minute budget and the clone is checked against the pack caps
  before it loads. A URL
  userinfo password or token refuses `bad-pack` before the clone and never
  reaches a printed line, a refusal message, or `verdict.lock`; the bare
  `user@host` forms keep working, and a hand-written lock row carrying one
  refuses too.

  `fetch`, `fetch-local`, `post`, and `record` now refuse `pack-lock-missing`
  when a repo pack has no lock entry and `pack-lock-drift` when the tree hash,
  pack directory, or `sig` disagrees, from one shared check with no bypass
  flag. `.verdict/verdict.lock` joins the controls inventory, so it is read at
  the PR base and an edited snapshot copy refuses `controls-stale`. `pack list`
  fills its `drift` column with null for a matching repo pack and the refusal
  code otherwise; the human rows show it, and a lock row whose pack directory
  is gone lists with `drift: "pack-lock-drift"`. The built-in pack stays pinned
  by the binary and org packs stay in per-operator settings, so neither is
  locked.

- 65dde0c: Add `verdict panel`, a local planner for independent verifier dispatches.
  Keep-biased results enrich findings and record/post/report summaries without
  voting, dropping findings, or exposing verifier evidence in posted bodies.
  New artifacts live under `verification/`; the pinned `panel` roster is unchanged.
  The verifier prompt joins the provenance hash; existing receipts retain their hash.
- 6f2f1ba: Post only comments and summary parts missing from fetched remote content. Recover partial publication after re-fetching, recognize identical findings at moved lines, and record new-head rearming with `rearmed` and `previous_head` in post output and receipts. Local receipts no longer block publication or override approval gates.
- 53fde33: Add opt-in PR summary comment support via `post --summary comment|off` (default
  `off`). When `off`, `post` output, host calls, and receipts stay byte-identical.
  When `comment`, `post` maintains one separate PR-level summary comment carrying
  a `<!-- verdict-summary head=<sha> -->` marker (distinct from review and
  finding markers, never editing the PR description), ignoring comments by other
  authors, updating the existing comment in place across GitHub (`PATCH`),
  GitLab (`PUT`), and Forgejo (`PATCH`), linking to the submitted review and
  posted inline comments where hosts supply URLs, and reporting duplicate matches
  in the receipt without deleting older comments. `--dry-run` reports the planned
  summary action (`create`, `update`, or `none`) without writing. A failed summary
  write never loses the accepted review's receipt: `summary.action` is `failed`
  with the reason, and the next post updates the summary.
- 22506c4: Infer the provider from origin when no provider flag is given, refuse unknown hosts, and record the selection source in PR.json.
- b70ea57: Add opt-in posted GitHub reaction proposals and per-rule acceptance and false-hit rates.
- 6b6d3fd: `ready` advises on GitLab merge requests and plain `calibrate` joins live
  GitLab threads, both through `glab` behind the same per-provider tables the
  GitHub reads already sit behind (GitHub output byte-identical). `ready` reads
  the merge request (`draft`, `has_conflicts`, `detailed_merge_status`,
  `description`, head pipeline) plus the pipeline and discussion lists, so the
  same draft, conflict, `ci-failing`, `threads-open`, and `description-thin`
  blockers report on either host; `calibrate` matches the posted discussion
  bodies (finding marker included) against the live discussions and reads
  changed files through the repository compare, so the same fixed,
  resolved-unfixed, open, and unknown outcomes join on either host. Bundles on
  the remaining hosts still refuse `provider-unimplemented` naming the host.
- 46c4591: Add advisory change-risk scoring (`review.json.risk`, `report --risk`,
  `report --max-risk`, and `trend` risk-band rollups). `review.json` accepts an
  optional `risk: {level, rationale, assessed_head}` claim validated during
  `record` and persisted to `record.json` / `post.json` (with stale-head detection
  via `risk-stale-head`). `report --risk` reports `claimed`, deterministic store
  `floor` inputs, and `reported = max(claimed, floor)` (keeping unassessed
  changes `unknown` and incomplete evidence at `>= medium`); `report --max-risk`
  adds an offline fail-closed gate dimension. `trend` adds per-band outcome counts
  under `by_risk` and `per_bundle[].risk`. Exposed across CLI, MCP `report`,
  Go SDK `Client.Report`, JSON schemas, and TypeScript client types.
- df3ddc7: Add per-rule `recalled_in`, `cited_in`, `exposure`, and `acceptance` columns to `learn --status` (text and `--json`), reusing the bundle overlap join and reporting missing recall manifests as `unmeasured`/`null`.
- 3acb87b: Import structured rule objects with `learn --pack <directory> --id <rule-id>`, preserving pack provenance and hash in the tracked KB controls.
- 7e03259: Launch your own agent. `verdict run <owner/repo> <pr|local-name> --agent <name>`
  runs the operator's agent CLI against an existing bundle — one process, one
  bundle, one shot — then asserts exactly one fresh `review.json` and hands off to
  the unchanged `record`/`post` validation. It never retries, re-prompts, repairs,
  or reads the model's prose, and it never posts.

  Agents are data: the shipped table names `claude`, `codex`, `cursor`, and
  `opencode` with their binaries and non-interactive argument shapes, so a new
  agent is a new row. Everything after a bare `--` passes to the agent unchanged,
  `--print` renders the exact command (POSIX-quoted) and exits zero without
  spawning, and `--timeout` sets the shared watchdog budget.

  Every failure is typed with a hint: `agent-missing`, `agent-spawn-failed`,
  `agent-timeout`, `agent-failed` (whose exit code becomes the process exit code so
  CI sees the real failure), `no-review-artifact`, `stale-review-artifact`,
  `unknown-agent`, and `agent-record-failed`. The launch is recorded in
  `.verdict/state/<bundle>/launcher.json` — agent, binary, argv shape with secrets
  redacted, timings, exit code — with the child's full output in `.run.out`.

  The verb is CLI-only: spawning a local agent is not an MCP tool, a facade method,
  or a TypeScript client method. Verdict invokes your agent only when you ask: no
  daemon, queue, scheduler, webhook, model selection, or interpretation of model
  output, and `run` itself makes no network calls.

- 052c2dc: `run` repairs an invalid `review.json` with `--rounds N` (1–5, default 1): round 1 sends today's directive unchanged, later rounds re-prompt the agent with the prior review bytes and the shape checker's fix list fenced under the run's nonce, and the loop stops on the first valid round. `--timeout` is per round, `--json` carries the loop in `data.repair`, and exhaustion refuses `repair-exhausted`.
- 930e677: Add live bundle invalidations to `verdict serve`, with `--interval`, `--sweep`, and `--no-watch` controls.
- a6f91a6: Add `verdict serve`, a read-only HTTP surface over the current repository
  (loopback by default; `--host` is the documented exposure opt-in):
  `/v1/health`, `/v1/roots`, `/v1/bundles`, the bundle detail, and `show`, with a
  per-port session-cookie exchange for the browser, strict CSP, Host and Origin
  gates, and `data.sources[]` traceability on every composed response.
- a9b1286: `serve --root` discovers nested repositories with bounded traversal and a local
  24-hour registry. The roots and inbox responses report cache, truncation, and
  skipped-pattern state.
- f2b8c96: Add `/v1/cost` repository, ISO-week, and model buckets plus `/v1/precision`
  category and tier counts from the stored thread feed. Unmeasured model prices,
  fixed outcomes stay `null`, no rates are published, and corrupt aggregates are
  marked partial.
- f43b7dd: `verdict serve` now opens an embedded browser UI with inbox, review detail,
  threads, cost, precision, and gate views. The UI supports dark mode, refreshes
  after live invalidations, and can read static `data.json` exports.
- 2f551c0: `verdict skill` grows its inspection and removal half. `skill show <path>`
  prints one embedded payload file byte-identically (with `--json`, one envelope
  carrying `path` and the same bytes in `content`); a traversal, absolute, or
  unknown path refuses `skill-path-not-inventory` and names `skill files`.
  `skill status [--dir|--agent...|--detect] [--diff] [--json]` is the read-only
  drift report: it writes nothing, reuses the install plan to report
  create/update/keep/blocked per target with the receipt versus embedded version,
  and exits 0 in sync and 1 when drifted, so CI can gate without
  `install --dry-run`. `--diff` adds one unified diff per changed file, capped at
  64 KiB overall with an explicit truncation marker and `truncated` in JSON.
  `--agent` now repeats on `install`, `status`, and `uninstall`: targets are
  deduplicated and handled in argv order, while `--dir` stays single.
  `skill uninstall [--dir|--agent...] [--force] [--dry-run] [--json]` deletes
  only the inventory paths the `.verdict-managed` receipt owns, then the receipt,
  and leaves every foreign file in place; a target that is a symlink
  (`skill-target-file`) or an inventory path that escapes the target through a
  symlinked directory (`skill-foreign-conflict`) refuses before any deletion; a
  target without a receipt refuses
  `skill-dir-foreign` unless `--force` (which still removes only inventory
  paths), an absent install refuses `skill-not-installed`, and `--dry-run`
  deletes nothing. `skill install` likewise refuses a symlinked target
  (`skill-target-file`) and a payload directory that escapes the target through
  a symlink (`skill-foreign-conflict`) before it writes anything.
  `verdict uninstall`'s skill-copy hint now names
  `verdict skill uninstall` instead of `rm -rf`.
- 9619fcf: Add `threads resolve <owner/repo> <pr> [--thread <id>]... [--dry-run]`, the
  receipt-backed thread mutation: without `--thread` it closes every open
  self-authored thread whose linked finding the local store marks addressed;
  with `--thread` it closes only the named ids, and a human-authored thread
  refuses. A thread is resolvable only when its root carries verdict's marker
  and its root author equals the authenticated token user (read once per run;
  an unreadable login refuses before any write); a forged marker skips as
  `not-self-authored` by default and refuses `not-verdict-authored` when named.
  `--dry-run` lists the plan and makes zero writes; a real run makes one
  host write per thread and records `threads-resolve.json`. GitHub resolves
  through `resolveReviewThread`, GitLab through the discussion resolved flag, and
  `thread_resolution` now reads true on both; Forgejo refuses typed because its
  API has no documented resolve endpoint.
- 471af44: Publish a shared truncation receipt across CLI `--json` `data` envelopes (`fetch`, `report`, `next`, `show`, `trend`, `threads`, and `serve`), bundle artifacts, and MCP text/structured results, and make `next`/`show` report truncated misses honestly instead of refusing or claiming nothing exists.

  Breaking (pre-1.0, within schema_version "1" by operator decision): index
  blast-radius lists replace the integer `truncated` count and `depth_truncated`
  with the shared boolean receipt (`truncated`, `truncation_reason`, `returned`,
  `total`, `next_offset`); complete results omit the receipt instead of carrying
  `truncated: false`.

- c3880e0: `verify` now filters the child process environment with a default-deny
  allowlist instead of forwarding the full parent `os.Environ()`. Only baseline
  locale, terminal, shell, XDG, and toolchain variables (`PATH`, `HOME`, `LANG`,
  `LC_*`, `TERM`, `TMPDIR`, `TZ`, `CI`, `USER`, `LOGNAME`, `SHELL`, color flags,
  `XDG_*_HOME`, Go/C/Rust/JS/Python toolchain cache and home variables, plus
  Windows OS runtime variables on `GOOS=windows`) pass by default, and any
  variable matching `envsecret.IsSecret` (such as `FOO_TOKEN`, `GITHUB_TOKEN`,
  `SSH_AUTH_SOCK`, or `LC_TOKEN`) is dropped even when it matches an allowlist
  prefix. An explicit opt-in keeps the named variable even when it matches
  `envsecret.IsSecret`. Operators can opt specific variable names in via
  `--env-allowlist <name[,name...]>` (also wired as `envAllowlist` on the `verify`
  MCP tool and `EnvAllowlist` on `verdict.VerifyRequest`) or `.verdict/verify-env`
  (one name per line, `#` comments and blank lines ignored, read from the
  base-snapshotted `controls` inventory rather than the untrusted PR worktree).
  Dropped variable names — never values — are reported in deterministic sorted
  order in `warnings[]`.
- 8428078: `verify --sandbox` runs the verify command inside an enforced bubblewrap
  sandbox on Linux: no network, a private PID namespace with its own `/proc`
  (so no host PID or `/proc/<pid>/environ` is readable), an empty `/run` (so
  `docker.sock`, the session bus, and agent sockets have no unix-socket route
  out), a fresh `/dev`, a private `/tmp`, `--new-session`, and
  `--die-with-parent`. `~/.aws`, `~/.ssh`, `~/.config/gh`, `~/.docker`,
  `~/.kube`, `~/.config/gcloud`, `~/.azure`, and `~/.gnupg` are mounted empty;
  `~/.netrc`, `~/.git-credentials`, `~/.npmrc`, and `~/.pypirc` are bound to
  `/dev/null`; the reviewed root stays writable. It probes the same flags with
  a no-op before anything executes and refuses `sandbox-unavailable` — running
  nothing and leaving `patch.json` and `.verify.out` untouched — on every other
  OS, when `bwrap` is missing, or when namespaces are restricted; it never
  silently falls back to an unsandboxed run. The opt-in reaches the MCP tool
  (`sandbox`), the Go facade (`Sandbox`), and the TypeScript client
  (`--sandbox` argv); the patch record, deep-group receipt, and `--json` data
  carry `sandboxed` when it was enforced. Runs without the flag are
  byte-identical to before.
- 7e03259: `post.json` records what a host withheld, so a degraded publish stays auditable
  after the envelope is gone. The new optional `withheld[]` lists one entry per
  capability the host could not carry, with the capability name and the same
  machine-readable reason code `fetch` publishes in `data.capabilities.unsupported`
  (`no-request-changes-event`, `no-approve-verdict`, `no-inline-anchors`, …) plus
  the `requested_event` the run asked for. A bound `REQUEST_CHANGES` on GitLab
  therefore leaves `review.state=commented` — the state the host accepted — _and_
  an entry saying `request_changes` was withheld, so it is no longer
  indistinguishable from a plain comment; the key is absent when nothing was
  withheld, and `schema_version` stays `"1"` (additive: no field renamed or
  removed). The `inline_anchors` capability now has a gate behind its claim: a
  host advertising `inline_anchors: false` is sent no anchored comment, and the
  merge keeps every finding in the body (keep-biased) with a warning naming the
  host, instead of writing a position the host would refuse.

### Patch Changes

- aa21d04: Check missing agent binaries before prompt assembly, include installation guidance, and restrict printed and recorded pass-through arguments to safe model and cost options.
- 1138177: Bound serve cache content hashing to 16 MiB per bundle lookup, shared across artifacts, chunks, and override fragments. Bypass the cache when the content budget is exceeded.
- f734e11: Hash bundle artifacts up to 64 KiB when validating the serve cache, detecting rewrites that preserve size and mtime.
- 934153e: Attach explicit context files with `fetch --context` and `fetch-local --context`, and cite context sources on findings.
- 2780859: The coverage read names what its percentage counts. `next --coverage` and the
  `coverage-incomplete` refusal said only "% of changed lines attested", while the
  evaluator scores every hunk span on both sides, context included, so a reviewer
  who attested every changed line but not its hunk context read a lower number
  than they had covered. Both read points now state the basis beside the number,
  for example `coverage: 54% of full hunk spans, both sides and context (12/22
lines, 1 files)`. The record warning stderr line uses the same basis, and the
  constant lives once in `internal/core/coverage` so the surfaces cannot drift.
  The `--json` keys are unchanged; the `warnings` string now names the same
  basis. The machine-built body line is unchanged.
- 9cc03a6: Host tokens no longer appear in curl's command line. Forgejo and Bitbucket
  reads hand the credential to curl as a config document on stdin, and a Forgejo
  write stages it in a 0600 file under a private temp dir because its body owns
  stdin; the file is removed after the call. The token, password, and
  `Authorization` header are gone from the argv a local `ps` or
  `/proc/<pid>/cmdline` can read. A credential carrying a newline refuses before
  any process spawns.
- 7262af7: Restructured the docs site into grouped sections with short sidebar labels, added a Markdown twin for every page, and pointed `verdict --help` at the new glossary route (`/verdict/docs/glossary`).
- 27e1cee: `gate --live` reads threads on every host and fails closed when it cannot. The
  live-thread dimension skipped the read for any bundle whose provider was not
  GitHub — it warned `live recheck skipped: <provider> threads unreadable here`
  and passed the gate without looking, so `--live strict` on a GitLab, GitCafe,
  Forgejo, Bitbucket, or Azure bundle was a pass that proved nothing. The probe
  now reads the bundle's own host through the same per-host adapter the `threads`
  refresh uses (GitHub keeps its resolution-only `gh api graphql` read, unchanged
  byte for byte), counts unresolved threads the same fail-closed way, and treats a
  read that stopped before the host's last page as unread rather than clean:
  `--live auto` warns and gates on the local dimensions, `--live strict` fails
  closed with the existing strict refusal. Only local bundles skip the lookup now,
  because they carry no host threads at all.
- 73ccf13: An unknown gate tier now refuses `bad-flags` everywhere, not only on the CLI. The MCP `report` tool's `flags.check`
  and the Go SDK's `Report.Check` and `Gate.MaxSeverity` used to pass any string through, so `check: "bogus"` gated on
  every finding and could flip an informational-only bundle from passing to failing. The CLI keeps its existing
  `--check` / `--max-severity` usage error.
- 0ec4587: The index accuracy corpus's four scanner bugs are fixed, so the published
  scorecard and the mechanism labels now match the manual. `index tests-for`
  names a test whose own package imports the declaring package at
  `transitive-import, depth 1` instead of dropping it, and `imports-module`
  resolves a relative specifier (`import { tidy } from "../lib"`) against the
  importing file's directory — including in a tree that also carries a `go.mod`,
  where a scanned package's import path is now the directory rather than the Go
  module path. `index callers` no longer attributes a Python attribute call
  (`log.save(...)`) to a same-named module-level function, and a Go selector
  written on a value outside the declaring package (`s.Save`, `a.Touch`) is
  counted in `counts.unresolved` instead of disappearing, so an empty answer no
  longer claims nothing was left unattributed.
- cfcd55e: Share the impact section byte budget across symbols, callers, and tests, with omitted counts for each list.
- ad7fcf7: Suppress module-function callers through import qualifiers shadowed by local bindings in Python and TypeScript. Rebuild indexes written with the previous scanner.
- c4359ac: Keep serve inventory accounting within its byte budget even for oversized artifacts.
- 1ff854e: Keep cited paths and comment identifiers inside quoted learning evidence.
  Refuse unsafe paths and identifiers before proposing or accepting a learning,
  including proposals generated before this validation was added.
- 5cf17ae: Refuse symlinks at every managed bundle component before writing, including links to targets inside the repository. Symlinked repository roots remain supported.
- 7e03259: Neutralize `@`-mentions in the review body and in each finding's inline
  comment. Both are written by an agent and published verbatim, so an `@handle`
  in that prose pinged that person the moment the review was submitted. `record`
  now escapes every mention once, where the text is constructed into the record —
  the same shared merge `post` runs — so the recorded body, each finding's posted
  comment, the `post --dry-run` output, and the payload digest carry the same
  bytes instead of diverging at submit time. A finding's signature is computed
  over the escaped bytes, so merging the same bundle twice keeps one signature.

  The escape is U+200B ZERO WIDTH SPACE between the `@` and the handle: invisible
  in rendered Markdown, still plain text, and no host resolves a mention across
  it. Fenced code blocks, inline code spans, HTML comments (finding markers
  included), and the basis/footer blocks are left byte-identical, and an address
  such as `user@example.com` is not a mention because the byte before the `@` can
  sit in an address's local part while the handle must be `[A-Za-z0-9-]+`. A
  finding's
  `suggestion` is code and stays byte-identical too — a zero-width space inside a
  patch would be an edit nobody can see. The rewrite is idempotent and has no
  flag: neutralization is not opt-in.

- cf9f5a0: `verdict next --coverage` with no positionals now leads with one `error:` line
  that names what is missing, like the other verbs. A plain `Basis:` line directly
  under the quoted `Confidence:` line is accepted without a `basis-replaced`
  warning.
- 7e03259: A `ranges: null` coverage claim is malformed, not whole-file. The attestation
  reader treated a present-but-null `ranges` as "no ranges given", which is the
  same shape as an omitted key, so it counted the claim as the file's whole hunk
  set: `record --dry-run --require-coverage` reported the diff 100% attested and
  the gate passed on a claim that carried no ranges at all. A `ranges` key that is
  present and is not a string — `null` included — now warns once and counts as
  unattested, exactly like a reversed or unparsable range. A claim still widens
  only by saying `"all"` or by omitting the key, so a malformed claim can never
  attest more than it says.
- b653391: `verdict pack add` reports a failed sweep of the previous tree as success plus a warning
  that names the leftover dot-prefixed sibling, instead of refusing an add whose new tree
  and lock are already durable. The loader already skips that sibling.
- 0bc5ef6: `verdict pack add` accepts only the three published git source forms — `https://host/path`
  with no userinfo, `ssh://user@host/path`, and `user@host:path` — and refuses every other
  remote-shaped string before any git call or write. The grammar is one allowlist shared by
  `pack add` and `.verdict/verdict.lock`, so the one-slash scheme, `%`-escaped passwords,
  token-only and two-`@` https URLs, `http://`, `git+https://`, IPv6 literals with
  userinfo, and a query or fragment (`?private_token=…`, `#…`) all refuse as
  `bad-pack-source`, and a hand-written lock row carrying one refuses
  as `bad-lock-source` at `pack list`, `fetch`, `post`, and `record`. No source is echoed
  raw: every refusal, human line, and `pack list` row renders through one display that
  prints a parsed source and `<invalid source>` for anything else. git's own failure text is
  never rendered: a failed clone, rev-parse, or checkout refuses as `pack-git-failed` with
  fixed wording for the phase and its exit code, because that text can carry the source URL,
  its query, or the ref.
  The published lock pattern follows the same grammar: it bounds each DNS label at the 63
  bytes the loader enforces and refuses a `://` inside an scp path, so a schema check and
  `pack list` agree on every source shape.
- bd8a2ca: `verdict pack add` refuses a crashed re-add's leftover staging or backup sibling as
  `pack-staging-stale` when it cannot sweep it, naming the path to delete instead of
  suggesting a `pack.json` repair. The loader still ignores those dotted siblings, so
  `pack list` and `fetch` are unaffected.
- d92d674: Compute post payload receipts from rendered review content and the reviewed head, excluding diff positions, event selection, and range metadata.
- 6a93e36: Refresh live remote reviews and comments before publication, refusing without writes if the read fails. Retrying a partial publication no longer requires re-fetching. Match identical comment bodies one-to-one, pairing exact anchors before moved lines so distinct missing findings are preserved.
- c242a67: Refuse to publish secret-shaped values. `post` scans the summary body and each
  finding's body and suggestion before the first host call, and a credential
  value — a PEM private key, an AWS key id, a GitHub, Slack, Google, Stripe,
  Anthropic, or OpenAI token — refuses `post-secret-shaped`, naming the site and
  the matched shape but never the value: a posted review cannot be unsent.
  `--dry-run` warns in the same words and still exits 0. `--allow-secret-values`
  publishes the review anyway, and `post.json` records the shapes it let through
  in `secret_shapes_allowed[]`. The patterns move to `internal/envsecret`, so
  `fetch`'s sensitive-content scan and this guard share one table; fetch's
  bundles and goldens are byte-identical, and so is the receipt of a review that
  carries no such value.
- aecbee1: Reconcile binding verdicts separately from summary and inline content. Retry failed approvals without duplicate GitLab summary notes, ignore Forgejo drafts, map Forgejo anchors before matching, and reject malformed nested live notes. Preserve verified patch provenance and clarify partial-publication retry advice.
- 7e03259: Mentions behind punctuation or emphasis are escaped too. The neutralizer opened
  a mention only at a line start or after a space or tab, so `(@alice)`,
  `**@alice**`, and `_@alice_` were published as written and still notified the
  person they named — the exact notification the escape exists to prevent. Any
  byte that cannot sit in the local part of an email address now opens a mention,
  so those shapes are escaped while `user@example.com` is left byte-identical.
  Fenced code blocks, inline code spans, HTML comments including the finding
  markers, and a finding's `suggestion` stay untouched, and the rewrite remains
  idempotent.
- b1e0da0: `record` (and `record --dry-run`) now warns once per `cost.json` `models[]`
  entry with no `name`, for example `cost.json: models[0] has no name — the name
is what attribution reads; add "name"`. The record stays accepted and priced —
  warnings only, never a refusal or rewrite — and `record --json` carries the
  same text in `warnings[]`. Only a missing or non-string name warns; an empty
  string passes, since the schema sets no minimum length.
- 1957a9a: `scripts/release.sh` no longer reports a shipped release asset as missing. The
  completeness check piped the asset list into `grep -q`, which exits at the first
  match; the writer then took SIGPIPE, `pipefail` failed the pipeline, and the
  asset landed in the missing list — so a complete release could refuse its own
  body ("release v0.6.1 is not complete (missing: …)"). The race lost only under
  load, which is why it showed up in a parallel eval run. The membership test is
  now a bash whole-line match with no producer that can break, and the
  `release-body` eval pins it with an asset list over 64 KiB whose first entry
  must still be found.
- bc1fbba: Export the six dashboard views with `report --html <dir> [--root <dir>]`.
  Refresh through sibling staging and backup directories, roll back publication
  failures, and refuse stale recovery artifacts or unsafe destinations.
- 13206b7: Publish live resolution metrics and save calibration receipts. Add offline
  resolution rollups, clean-review metrics, and clean report headlines.
- f647837: A batch of small fixes from recent review rounds:

  - An agent that exits non-zero just before its time budget runs out is now reported `agent-failed`, not
    `agent-timeout`. The watchdog records its own budget kill instead of comparing the clock afterwards.
  - MCP refusal results carry the refusal's `warnings`, both as `warn:` lines in the text and as a `warnings` field in
    the structured result.
  - Human-mode output prints a refusal's warnings as `warn:` lines before `error:`, so `pack list` no longer hides a
    stray-file warning when it refuses.
  - The `index-capped` refusal names the bytes it measured. Its hint no longer suggests raising a cap that cannot be
    configured.
  - `index status` points at the query, not at `index build`, once HEAD is indexed.
  - Reading a crashed agent's partial review can no longer block on a FIFO.

- 7e03259: Keep an argument credential out of the launch record. `verdict run --agent`
  redacted a secret-shaped flag's operand in `launcher.json`'s `argv`, but the same
  credential still reached the bundle twice: the recorded `output_tail` was scrubbed
  only against secret-named environment values, so a child that echoed its own argv
  — or printed it in a stray debug line — left the operand in plaintext, and the
  raw child log `.run.out` was written `0644`.

  The output scrub now hides the operand the argv redactor already identifies, from
  one walk that feeds both the record's `argv` and its tail, so the two cannot
  disagree about what a credential is. `.run.out` stays the child's raw stream for
  debugging and is written `0600`. The environment scrub, the tail bound, `--print`,
  and `launcher.json`'s shape are unchanged.

- 430d6f8: Add a bounded local self-review recipe for Claude, Codex, and OpenCode, with round receipts, offline evals, and benchmark instructions. Require complete recorded coverage, bound to the verified diff identity on dirty local bundles. Refuse stale dirty-tree coverage after tracked files change since fetch.
- e4c4da5: Pass setup-verdict inputs through environment variables and cache release archives. On reuse, refresh the signed checksums and build-commit
  metadata, verify the archive, then extract a fresh binary without downloading the
  archive again. A tampered archive fails closed and remains cached until the entry
  is removed. Cache reuse requires release metadata access.
- 8205d58: `verdict skill install` now checks its targets before the first write. On POSIX
  exactly the directories an install will write are probed before the first byte,
  so a read-only skills dir anywhere in a `--detect` set refuses with
  `skill-install-failed`, names that target, and leaves the earlier targets
  absent instead of already installed. A target that changes between that probe
  and the write still fails, at write time. Windows reports that failure at write
  time only, because its writability probe is a stat. A receipt path that cannot
  be replaced refuses with `skill-foreign-conflict` the same way, before the
  payload lands. A single-target install refuses on the same probe, and a
  repeated `--agent` dedupes to one install per target instead of refusing.
- 7e03259: Mentions wrapped in strikethrough are escaped too. The neutralizer walked back
  over a run of `*` or `_` to tell emphasis markup from the tail of an address,
  but `~` is also a valid local-part byte and was left out of that walk, so
  `~~@alice~~` was published as written and GitHub read it as a strikethrough
  mention — the exact notification the escape exists to prevent. A run of `~~` now
  counts as markup the way `**` does, so that shape is escaped while
  `foo~@example.com`, `user@example.com`, `(dev@example.com)`, and a lone tilde in
  prose such as `approximately ~5 minutes` stay byte-identical. The rewrite
  remains idempotent, and fenced code blocks, inline code spans, HTML comments,
  and a finding's `suggestion` are still untouched.
- 023a267: `docs/troubleshooting.md` lists every refusal code the binary can emit, each
  with its cause and its fix. A drift test fails the build when a code ships
  without an entry (or an entry names a retired code), and the manual's
  refusal section links the page.
- 2430e16: `verdict upgrade` quotes the project-local `run: cd <root>` hint with the
  POSIX single-quote rule, so a project path with a space or `'` pastes
  resolvably; `verdict uninstall`'s sibling skill-copy refusal now reads as a
  sentence (`remove it with the tool that installed the skill copy at <dir>`)
  instead of rendering prose in the `remove it with:` command slot.
- 11f436b: Treat a thread feed removed during a serve read as absent instead of corrupt.
- 1cf24d9: The verify pre-exec gate closes twelve static bypasses. `wget --remote-name`,
  `wget -i`, `curl -K`/`--config`, `busybox wget`, `pip3 download <url>`,
  `telnet`, curl's `telnet://` scheme, and `git archive --remote` now refuse as
  `verify-networked`; a secret-shaped variable from the shared `envsecret`
  classification in a networked command (`curl .../$AWS_KEY`,
  `curl -H "X: $AWS_KEY"`) refuses as `verify-credential-read`; and
  `git --exec-path` plus `tar -I` refuse as `verify-destructive`. Both short
  options match the attached and clustered spellings a shell accepts too
  (`tar -Itouch-fz`, `tar -cIf`, `wget -i/tmp/urls`, `wget -qi /tmp/urls`), not
  only the separated form. Every refusal keeps the quoted fragment and `try:`
  rewrite hint. The documented boundary is
  unchanged: a plain `curl` GET, `/tmp` scratch writes, and non-secret variables
  in URLs still execute, and the adversarial corpus rows that pinned each gap
  move from `xfail` to `strict` with benign neighbours beside them.
- 7e19070: On Windows, a command that failed to start — never attached to its job object,
  or left with no thread to resume — and then survived verdict's cleanup kill
  left the run waiting on it forever. That spawn failure now returns at once,
  naming both the spawn error and the kill error, instead of hanging.
- 5d3372a: Refuse symlinked bundle directories before `post` or `record` writes.

## 0.6.1

### Patch Changes

- a5870d1: The npm wrapper and each platform package now ship the repository README, so the registry page renders the project documentation instead of an empty README.
- 4d7fb67: The package page now names what the tool does rather than a claim that stopped being true: the description drops "zero install" (every route installs a binary) for the loop it actually runs — fetch, review, gate, post — plus cost rollups and one static binary, and the package carries the docs site as its `homepage` instead of letting npm fall back to the repository README. Keywords gain `cli`, `mcp`, `go`, `llm`, `static-binary`, and the agent vocabulary, and drop the self-referential `verdict`. The docs site's own description moves to the same sentence, so the About box, npm, and the site stop disagreeing.
- 88508a4: Public surfaces now match the binary. The docs site, its home pages, and the CI review example use `.verdict/state/`; the operator manual documents deep validation, path-scoped profiles, the evidence ledger, posting guards, local review coverage, and the shipped CLI flags, and states that `verification.json` is agent attestation rather than tool proof; the TypeScript client throws a typed `VerdictGateError` for a gate-shaped exit 1 instead of a schema error; the Python smoke uses the real bundle path and runs in CI; the Go SDK exposes record dry-run/strict/cost-names and fetch/fetch-local head/worktree controls; `config --explain` human output shows privacy exclusions and plan group ids; `spend-capped` carries a hint; and the README says the Go SDK runs in-process.

## 0.6.0

### Minor Changes

- e59d899: **Breaking (cost record):** `cost.json` accepts one `models` shape: an array of entries. A record whose `models` is an object is no longer read by key order — that coercion dropped the keys, which are the only place the writer named a model, so the entry priced without anything able to attribute it. Such a record now reads as unreadable spend, named by the existing `cost.json unreadable — spend unrecorded for this rollup` warning, and the run still completes. Every record the shipped `prompts/review.md` template writes already carries the array form, so no template and no documented recipe changes. An existing object-valued record migrates by rewriting it as `"models": [{"name": "<panel slug>", "model": "<model id>", "input_tokens": N, "output_tokens": N}]`; `record --cost-names` will not convert it, because converting would restore the shape the reader just dropped.
- c48c94d: `record --cost-names[=<names>]` names the `models[]` entries of a bundle's cost record and its per-chunk records, in place and without changing a count. Names come from the flag's own ordered list — synthesizer first, then lenses — or from a panel pinned in the bundle; with neither, the repair refuses rather than inventing a name from a slug.

  An entry that already names its seat keeps that name. A name that sits on the wrong seat, repeats, or comes from outside the panel refuses with a code naming the entry and the seat it should hold, and an entry the panel cannot explain refuses rather than being left half-attributed. A document holding several records gives each record the whole panel, never a share of it.

  The repair plans every cost file before writing any, and it runs after the record verb's other checks, so no refusal rewrites a cost record. A cost file that changed after it was planned refuses rather than replacing the newer write, and a record that grew past the reader's 8 MiB cap refuses before anything is written. A write that fails mid-pass stops with the files already written named in the refusal, and re-running finishes the repair.

  This verb is how an operator repairs the array that the same release made the only shape: a record whose `models` is an object is `cost-models-not-array` here rather than converted, because converting would restore the second shape the reader just dropped. Migrate by writing an array whose entries name their seat — the migration note sits with the array-shape change, and this verb is its repair path.

- b7761d2: Lens certification becomes version-scoped, and aggregation stops refusing records it can read. A cost record offered as `schema_version: "2"` may carry only the top-level fields v2 defines: an extra field now refuses by name and version (`carries top-level field "notes", which that version does not define — remove the field; no version this build certifies defines it`) instead of a generic "unreadable" sentence, a record bound to the run at a version this build does not certify refuses as `version-unsupported` rather than being re-read as v2, and a `schema_version` no reader can parse (`null`, a boolean, an array, an object, an empty string) is `malformed` instead of counting as absent — so an unreadable declaration can never let an older certified record stand in for the newest binding. The group estimate and the cost rollup take the other side of the split: they price the fields they know and ignore top-level fields a newer writer added, so one run's stray key no longer voids the history an estimate is built from — that closes a `budget-unknown` refusal under `require_known_estimate` that a single unregistered field could previously trigger. Nested shapes stay strict, and every fail-closed check still fails: foreign capture, duplicated key, unreadable entry, mismatched binding.

### Patch Changes

- 8423f7a: `report --cost` keeps a chunk's spend when the bundle's own `cost.json` is unreadable. The broken top-level record used to be folded into the same rollup as the per-chunk records, so one bad file voided every chunk beside it and reported zero tokens for a run that had real measured spend. It is now dropped on its own — named once by `cost.json unreadable — spend unrecorded for this rollup`, the same way an unreadable chunk is named — and the chunk records still price. No other shape changes: a bundle with no chunk records reads its top-level record exactly as before.
- 2b08c77: The release body is asserted, not assumed. `scripts/release.sh` now reads the published release back after GoReleaser publishes the assets, rewrites the body from `CHANGELOG.md`'s section for the tagged version when it differs, and fails the run when the rewrite does not stick or the release is incomplete. A body that differs only in the whitespace GitHub hands back is accepted rather than rewritten, and a release that is missing assets never gets a body at all. Release 0.5.0 shipped a one-byte body while the notes file held 15.9 kB and every publish step reported success; the new `body` stage closes that gap, the `release-body` eval holds it offline, and a re-run leaves an already-correct body untouched.

## 0.5.0

### Minor Changes

- 027d29a: The bench runner learns a repair loop. Pass `-loop N` to let one sample try up to N rounds: the harness runs `record --dry-run --json`, feeds the problem list back, and records only the last valid round. Round evidence lives under `runs/<slug>/round-NN`, the report gains `loops`, `fix_rates`, and `loop_cost`, and `-loop 1` keeps the old single-shot files byte-identical.
- 110dda8: Findings can now carry a closed category (`logic`, `edge-case`, `security`,
  `performance`, `accidental-commit`, `style`, `docs`, `other`): an optional
  per-comment label that never changes the finding's signature. `report
--category` filters to one class, `report --check --category` gates only that
  class, and `report`, `calibrate`, and `trend` all publish per-category counts
  (untagged for unlabelled findings). Unknown or non-string values warn once
  and store null; they never refuse a review.
- 3dbe206: Findings can now carry a repair brief: the affected sites, a one-sentence
  outcome, an optional approach and open questions. `record` validates it against
  the fetched change and keeps it across re-records, `show --json` and `fix`
  print it, and it never changes a finding's signature. A malformed brief warns
  and is dropped; it never refuses a review.
- 3bd05e9: `verdict gate` is one composed CI gate over the three dimensions `report`
  already computes: open findings at-or-above a tier (`--max-severity`), a
  completed post for the fetched HEAD (`--require-head`), and live unresolved
  threads (`--live auto|strict|skip`). One exit code carries the answer — 0 pass,
  1 fail-or-unknown, 2 usage, 3 tool error — and the human tail names each
  dimension, the tally, and one `try:` hint covering every failing dimension.
  `--json` adds `data.passed` plus `data.dimensions[]` in `findings, head,
live_threads` order with `count` null where a dimension has no tally; a failed
  gate keeps `error: null` because a verdict is not a refusal. `gate` is
  read-only (no store writes, no post) and absent from MCP: a local pass is not
  authority to merge.
- 3089864: **Breaking (experimental MCP):** a host on `verdict mcp --read-only` no longer
  sees a `threads` tool, and `report` there drops its `--output`/`--sarif`
  arguments and reports `readOnlyHint: false` instead of claiming to write
  nothing. Seven tools remain: `report`, `next`, `show`, `trend`, `ready`,
  `doctor`, `check-tree`. A host that needs the thread feed should refresh with
  `verdict threads` outside the server or run it without `--read-only`; a host
  pinning `report` as a read-only tool should stop trusting that hint.
- eb2572b: Patch identity dedupes re-reviews across rebases. `fetch` and `fetch-local` write `.verdict/state/<bundle>/identity.json` (`git patch-id --stable`, labeled `diff-sha256` fallback), `post` refuses `already-reviewed-patch` on a same-patch sibling unless `--force` (recorded in `post.json` `forced[]`), `adopt --from auto` matches exactly one local twin, and `report --completeness` passes when the posted and fetched heads differ only by a rebase with the same patch.
- cdb5a61: `verdict record` can now answer "would this record?" before writing anything.
  `record --dry-run` runs the same checks and writes nothing, returning a
  summary of the comments, sections, and anchors that would move to the body.
  Both modes share one checker instead of stopping at the first problem: the
  refusal carries `error.problems[]` in document order with a JSONPath, a
  published `code`, the level, the message, the exact `fix`, and an optional
  `example`, plus `error.problems_truncated` past the 50-problem cap; human mode
  lists the same problems before its try/re-enter lines. The checker owns the
  brief, anchor/side, kb-id, chunk-attestation, confidence, security, claim,
  triage, section, and verification-input checks, not just the top-level shape.
  Warnings (moved anchors, brief parts dropped without refusing, malformed kb
  ids, unknown fields, an unset rigor) let the record proceed unless `--strict`
  or `"strict_input": true` in `controls.json` promotes them.
  `pkg/verdict.CheckReview` exposes the pure checker, the MCP `record` tool gains
  `dry_run` (served on a read-only server; a non-dry record is still refused),
  and the review skill now loops write -> dry-run -> fix for at most three
  rounds, then reports any remaining problems instead of recording a partial
  review.
- 05125d5: `verdict record` now repairs the mechanical shapes weak models emit instead
  of refusing them. Before checking, `NormalizeReview` unwraps one
  whole-document code fence, drops prose around exactly one balanced JSON
  object, wraps a scalar where a string array belongs, renames the
  `inBounds`/`rung`/`file` aliases, folds rigor synonyms and enum case, and
  removes a BOM, an explicit null on an optional key, or an exact-duplicate
  comment. Every rewrite is a warning with its own code, listed in
  `record.json`'s `repairs[]`; `--strict`, `REVIEW_STRICT_INPUT=1`, or
  `"strict_input": true` in `controls.json` turns each into an error.
  `record --dry-run` returns the exact stored object as `data.normalized`
  plus `data.repairs`, and never rewrites `review.json` on disk. The CLI also
  owns the body's `> Basis:` line, replacing a disagreeing line (or inserting
  a missing one) with the computed counts and reporting `basis-replaced`.
  Unset rigor records as `said` with `rigor_source: default`, and length and
  numeric bounds now refuse as `out-of-range` instead of `bad-enum`.
  `pkg/verdict.NormalizeReview` exposes the pure, idempotent primitive.
- 3089864: **Breaking:** the published schemas moved with the skill, so every schema's
  `$id` base path is now `skills/verdict/schemas/` rather than `schemas/` —
  `https://github.com/stainless-code/verdict/blob/main/skills/verdict/schemas/envelope.schema.json`,
  for example. If you resolve a schema by URL, or pin `$id` in a validator, update
  the base path; the schema contents are unchanged.
- bdc9938: The Go SDK and the TypeScript client can read review-thread state in-process,
  so the one review read an embedder had to shell out for is now a method.
  `verdict.Threads` wraps the `threads` verb: `ThreadsRequest` carries the repo,
  PR, and `--provider` override, and `ThreadsResponse` returns the envelope plus
  the refreshed feed tally (`Dir`, `File`, `Provider`, `HeadSHA`, `FetchedAt`,
  `Truncated`, `Threads`, `Open`, `Outdated`, `Verdict`), with `Data`
  byte-identical to `threads --json`. The TS client gains `threads(...)`
  resolving `Envelope<DataFor["threads"]>`, and every review verb now has a typed
  client method instead of falling back to `run()`. No wire or behavior change:
  the verb, its data profile, and its MCP tool already existed.
- 9cb4bfd: `verdict skill install --detect` adds the review skill to every coding agent
  already set up on the machine, instead of making you look up one path per agent.
  It names each target and its result, and `--json` lists each target with its
  status, path, and files written. With no agent set up it refuses and points at
  `--agent <name>`, and `--detect` is mutually exclusive with `--dir` and
  `--agent`. `install.sh` and `install.ps1` now close a successful install with
  that exact command.
- 7fce8bd: The TypeScript client's verb methods now return their own `data` profile
  instead of `Envelope<unknown>`: `ready` resolves `ReadyData`, `fetch` resolves
  `FetchData`, `trend` resolves `TrendData`, and so on for every verb method.
  `DataFor` exposes the same verb → profile map, so code that passes `data`
  around can name the profile without restating it.

  Every profile is generated as a `<verb>Data` `$defs` entry in
  `envelope.schema.json` (with the nested `$defs` it references), so non-TypeScript
  clients get the same shapes from the published schema. Additions only: `data`
  stays an open object that allows additional properties, no existing property,
  required key, or type changed, and `client.run()` still returns
  `Envelope<unknown>` for verbs without a profile.

- e4ed6a1: Add typed configuration in `.verdict/config.json`, with path scopes, captured explanations, review profiles, grouped spend and diff limits, and recorded deep validation. Local run artifacts now live under `.verdict/state/`; retired `.review/` knob files are no longer read.

  Cost records use schema version 2 with grouped lens and cost evidence. The Go Commander interface now takes a command pointer and preserves its working directory.

### Patch Changes

- 0c54ab2: Corpus cases accept an optional `also_lines` list of alternate anchors for
  the same defect. Each entry is matched with the same line tolerance and
  defect-naming rule as the primary line, matching any anchor counts the bug
  once, and the reported line stays the primary one. The generated
  `corpus.schema.json` now requires positive line numbers in `also_lines`
  items, so schema validation rejects `0` and negative entries just as
  `calibrate` already refused them as `corpus-invalid`.
- 72db325: `calibrate` and `trend` grade a corpus finding on the defect it names, not the
  line it picked. A finding used to count as a true positive whenever its path
  matched and its line sat within `line_tolerance` of a known bug, so on a
  four-to-seven-line hunk every anchor was correct and the reported precision
  measured line-guessing rather than review quality; the same line could hold a
  comment about variable naming and score as a catch. A true positive now also
  has to name the bug: one of its `keywords` (new, optional in
  `corpus.schema.json`), its `kind`, or a phrase from the published category
  vocabulary. The default window drops from three lines to one, and a finding `record` moved out of its hunk is
  never a true positive, so the anchor is measured and not assumed.

  The human false-positive line says which rule failed, so a run distinguishes a
  finding that landed off the bug from one that landed on it and described
  something else. Corpus bundles the benchmark runner writes now carry the
  reviewed `full.diff` and its `hunks.tsv`, so `record` scores against the hunks
  the agent read instead of rebuilding an index from a diff that may have moved.

- 58dbc7d: The docs site publishes its documentation guide as the `verdict-docs` skill
  instead of a second skill named `verdict`. The `verdict` name belongs to the
  review workflow — `skills/verdict/SKILL.md`, the payload `verdict skill install`,
  `npx skills add stainless-code/verdict`, and the embedded binary ship — and a
  harness that indexed both well-known skills loaded whichever won, handing an
  agent that asked how Verdict works the review workflow, or the reverse. The
  site's `/.well-known/agent-skills/index.json` now lists one skill,
  `verdict-docs`, and the docs build fails if two published skills ever share a
  `name:`.
- 3089864: Global flags work anywhere on the command line — before the verb, between the
  verb and the repo, or after the pr — so `verdict --no-json report acme/app 9`
  and `verdict report acme/app 9 --no-json` print the same output. A bare `--` now
  ends positional parsing, so everything after it is a positional and
  `verdict fetch-local acme/app -- --json` is a usage error instead of a stray flag.
- 0e8ed37: `verdict mcp config --agent codex --write` reads its `config.toml` the way TOML
  does, so a `verdict` table, key, or comment inside a string is text and no
  longer a merge target: a file that ends inside an unterminated string, or whose
  `[mcp_servers.verdict]` line opens one, refuses typed and keeps its bytes
  instead of having keys appended into the literal. A table spelled inline or
  dotted refuses rather than guess, the file's own CRLF or LF line endings are
  kept, and an append no longer drops a trailing line of non-ASCII whitespace.
- 72d0a60: `verdict post` on a PR that was never fetched now refuses with the typed
  `missing-bundle` error naming the fetch command, instead of failing internally
  on its first write.
- 1b16049: Every release now publishes `commit.txt` — the commit SHA the tag points at and
  the certificate's workflow-SHA claim — and `install.sh`, `install.ps1`, and
  `verdict upgrade` pin `cosign verify-blob` to it with
  `--certificate-github-workflow-sha`; the `setup-verdict` action inherits the pin
  through `install.sh`. The identity and issuer alone accept any run of the
  Release workflow, so a bundle from a different build of the same workflow used
  to satisfy the check; the pin makes that a refusal. A release that publishes no
  `commit.txt`, or a `commit.txt` that is not a full 40-character SHA, is refused
  with `verify-failed` instead of being installed unpinned.
- 3089864: A signed install no longer aborts when `cosign` is on the `PATH`: `install.sh`,
  `install.ps1`, `verdict upgrade`, and `setup-verdict` now verify the certificate
  identity the Release workflow actually mints, which is a push to `main` rather
  than the tag the run publishes under. The old check asked for a `refs/tags/…`
  identity that no release certificate ever carried.
- 3089864: The shipped review prompts no longer hand an agent a corrupt skeleton to copy.
  The "Technical details" block and the mermaid wrapper had a fence opened mid-line
  with its body flattened onto it, plus orphan closers and an unclosed block, so an
  agent following them emitted run-together headings and an unterminated fence.
  Both are well-formed blocks now.
- c27ea2f: `npx skills add stainless-code/verdict` now installs just the skill instead of
  copying the whole repository — over a thousand files — because `SKILL.md` and its
  payload moved into `skills/verdict/`. That directory holds exactly the files
  `verdict skill install` writes.
- 3089864: The skill now triggers on every review target it can handle — a pull request, a
  branch, a commit range, or staged changes — so an agent picks it for local
  reviews too. `NOTICE.md` carries the full MIT text of the upstream review
  prompts Verdict adapts, pinned to the upstream commit, and the skill copy under
  `skills/verdict/` ships that same license beside the prompts it covers.
- 3089864: The TypeScript client's generated types render `anyOf` as a union instead of
  collapsing it to `unknown`, and a null variant folds into a trailing `| null`
  the same way a `"null"` entry in `type` does. `show`'s `data.fix.verify_hint`
  now types as `VerifyHint | null` rather than `unknown`.
- 3089864: `verdict upgrade` recognizes an install made with `VERDICT_INSTALL` even after
  the variable is gone, by reading the installer receipt beside the running
  binary. A receipt is a hint, never proof of ownership: when npm, Homebrew, a
  project's `node_modules`, or a skill copy already owns the directory, `upgrade`
  and `uninstall` keep that channel and delegate or refuse with the owning command
  instead of replacing or deleting a file the manager tracks.
- d9366dc: Shell completion now offers `threads`, with its `--provider` and `--json`
  flags.
- 22f8e00: `verify` now refuses a path glued to the option that takes it: `tar -C/etc`,
  `cp -t/etc/cron.d`, `sort -o/etc/x`, `grep -f/etc/passwd`, `gcc -I/usr/include`,
  `git -C/etc`, and `go build -o/etc/x` exit before the command runs instead of
  executing it. The pre-exec gate only recognized a path after a space or a
  separator, so every spelling getopt accepts — a clustered `-czf/etc/x`, a
  positional `-C` after `--`, a quoted or backslashed `-C/etc`, an uppercase
  `-C/ETC` — reached the shell with the escape unreported. An attached value is
  now judged as the path it is, and the refusal names the fragment it read.

  One spelling to know: a relative value that starts with a letter and contains a
  slash (`git -Ctools/cli`) has the same shape as `-C/etc`, so the gate refuses
  it rather than guessing. Write `git -C tools/cli` — both spellings of a relative
  value stay allowed once a space separates the option from its value.

- 32d60ce: A canceled re-verify keeps the last completed proof, and a failed Windows handoff leaves no staged helper behind.

## 0.4.0

### Minor Changes

- 3c64fc7: Measured review rates instead of impressions. A marked `corpus-*` bundle kind
  carries a checked-in case manifest with ground-truth bugs and provenance;
  `calibrate` grades a model's findings against that ground truth offline and
  reports precision, recall, and false-positive rate, and `trend` aggregates the
  same numbers per model and panel. Ordinary `local-*` reviews keep refusing the
  live join. Ships a seeded starter corpus under `bench/corpus/` and
  `docs/benchmark.md`, an operator runbook for real PRs, real agents, cost
  estimates, and publishing measured rates.
- b21eaff: Experimental MCP server. `verdict mcp` serves the review verbs to an MCP host
  over stdio (newline-delimited JSON-RPC 2.0): no daemon, no HTTP, no new
  dependency, sharing the host's credentials and exiting when stdin closes.
  Tool schemas reflect the CLI request types, results are the `--json`
  envelope's `data`, and typed refusals return `isError` with code, message, and
  hint intact; `--read-only` exposes only the non-writing verbs, while `post` is
  available in normal mode and in `--operator` mode. `verdict mcp config --agent
claude|cursor|codex` prints the exact host entry, and `--write` merges it
  idempotently into the documented config (`.mcp.json`, `.cursor/mcp.json`, or
  `$CODEX_HOME/config.toml`), backing the old file up to `<path>.verdict.bak`
  and refusing unparseable or foreign entries instead of rewriting them. `--json`
  prints one envelope, never prompts, and never starts the server; `verdict
--help` now lists the MCP entry so the surface is discoverable.
- b21eaff: `verdict` is one static binary — no checkout, no `jq`, no shell script. Install
  it with the verified `install.sh` (macOS/Linux) or `install.ps1` (Windows) into
  `~/.verdict/bin`, the Homebrew tap, or the npm wrapper; both installers verify
  the cosign signature and the archive SHA-256, smoke-test the installed binary
  from its final path, and add `PATH` with one marked line
  (`VERDICT_NO_MODIFY_PATH=1` opts out). In CI the new `setup-verdict` action
  installs a pinned, verified copy on Ubuntu, macOS, and Windows runners without
  touching shell rc files. `verdict upgrade` updates whichever channel installed
  it — a verified self-replace for installer installs, the owning package
  manager's global update for npm and Homebrew — and `verdict uninstall` removes
  only installer-owned files; both refuse, naming the exact command, when the
  install belongs to something else. `verdict skill` prints or installs the
  payload embedded in the binary (`SKILL.md`, `prompts/`, `schemas/`, and the
  `scripts/verdict` shim), so the served skill can never skew from the binary.

  **Upgrading from 0.3.x:** the old script install is replaced, not patched.
  `scripts/review.sh` is gone — every verb is `verdict <verb>` with the same
  flags and `--json` envelopes. The old symlink-only `install.sh` became
  `install-skill.sh` (checkout-only), and the copy-based `install` verb is gone,
  so reinstall the skill with `verdict skill install` or `npx skills add`. Docs
  and scripts that call the old script path must be updated. Start at the
  rewritten quickstart, the new glossary of review terms, the CI recipe in
  `docs/ci-recipe.md`, and `verdict --help`, which now names each verb's
  provider scope.

- b21eaff: Posting is harder to get wrong and harder to do twice. `post` refuses a second
  review on the same reviewed SHA before any network read, naming the exact
  `fetch --base-sha` advance; it also refuses duplicates found in the bundle's
  own fetched review list, so a fresh checkout, a CI re-run, or another machine
  cannot repost what the PR already has — `--force` still overrides, and the
  refusal names the re-fetch needed when the earlier review was deleted. Points
  outside the diff hunks post under "Unanchored observations" instead of as
  inline anchors GitHub would reject. A body past the 65,536-character hosting
  limit splits into follow-up COMMENT reviews — first review keeps the footer,
  later parts carry a continuation header, and a failed follow-up refuses typed
  naming the review that already landed — instead of being trimmed. A 403/429
  carrying a rate-limit signal retries within bounds, honoring `retry-after` or
  the reset marker and never publishing twice; when the last permitted attempt
  is still retryable, the refusal carries `retry_at` in RFC 3339 so an agent can
  wait for that instant.

  A stale-fetch downgrade note is now followed by a blank line, like the drift note, instead of a stray literal `\n` in the posted review body.

- 9fb0cad: The PR conversation is a local, schema'd record instead of something each verb
  re-reads from the host. Every fetch writes `.review/<pr>/threads.json` (schema
  published) holding each thread's host id, diff anchor, resolved and outdated
  state read from the host's own fields, root author kind, and the comments with
  authors, timestamps, and capped bodies; GitHub comes from GraphQL
  `reviewThreads` (REST carries no resolution), and GitLab, GitCafe, Forgejo,
  Bitbucket, and Azure fill the same shape, paging rather than truncating.
  `verdict threads <owner/repo> <pr> [--provider …] [--json]` re-reads just that
  surface and rewrites only that file, so re-review, address-reviews, readiness,
  and calibration can join prior feedback on any of the six hosts. Posted inline
  comments now carry a hidden final-line `<!-- verdict-finding sig=… -->` marker,
  so a new comment links back to the finding that produced it while older
  comments keep reading as unlinked.
- b21eaff: Published machine contracts beside the CLI. `schemas/` ships JSON Schemas for
  the review input, findings store, cost and usage records, PR threads, and the
  `--json` envelope with per-verb `data` profiles, included in the npm tarball;
  `schemas/validate.sh` checks documents against them offline (no `jq`; exit 0
  valid, 1 rejected, 2 bad usage), and the drift rule is additive within schema
  version 1. An experimental TypeScript client
  (`@stainless-code/verdict/client`) wraps each verb with envelope types
  generated from those schemas and refusal codes generated from the CLI, with no
  runtime dependencies. The experimental Go package `pkg/verdict` embeds the
  same operations in-process through a context-first client with typed requests
  and responses and a `*RefusalError` for `errors.As`; its API may still change
  before 1.0. A new "Build a client" guide documents the `--json` protocol —
  envelope keys, exit and refusal codes, versioning, binary resolution,
  timeouts, output caps, and when MCP beats spawning a process — with a
  dependency-free Python client generated from the schemas and an offline smoke
  test.
- b21eaff: The readiness gate can say what it knows. `report --check` and
  `report --completeness` gain a live-thread dimension: open threads the local
  store does not know about fail the gate, and unreachable lookups warn and stay
  neutral. New `--live auto|strict|skip` picks the posture — `auto` keeps that
  fail-open default, `strict` fails closed when the host cannot be reached, and
  `skip` pays zero network and gates on the local dimensions only, saying so in
  the output and the `--json` warnings. `post` and the gate now name the same
  advance for head drift, and a binding APPROVE on a known-stale fetch
  downgrades to COMMENT instead of approving past the moved head.
- 21ac180: Chunked split-range reviews. Very large PRs are reviewable again: with
  `--chunk` (or `.review/chunk`) an over-cap diff is sliced on file boundaries
  into numbered chunks with a versioned manifest, the agent reviews one chunk at
  a time, and the results synthesize into one verdict with a summed Basis line.
  `post` and `record` refuse coverage that claims to be complete but is attested
  partial, and warn loudly when a chunked review is unattested. `fetch-local`
  supports the same flow.
- 21ac180: `verify` refuses before it runs. Destructive, networked, credential-reading,
  and repo-escaping commands are rejected with a typed error before execution
  (over 40 regression shapes), so a hostile or careless `verifyCmd` cannot
  delete, exfiltrate, or escape the repository. The recorded tail also drops
  credentials no environment value names — vendor-prefixed tokens,
  `Authorization` and `Proxy-Authorization` headers, bare `Bearer` values, and
  URL userinfo — and strips ANSI escapes and other control characters, so a
  command cannot rewrite the proof it lands in; `.verify.out` keeps the raw
  bytes. `doctor` warns on excess token scope, and the manual documents an
  opt-in hostile-PR sandbox recipe plus the residual risk a string scan cannot
  remove.

### Patch Changes

- b21eaff: Cost accounting reads what the operator actually knows. `cost.json` entries
  gain optional `cache_read_input_tokens` and `cache_creation_input_tokens`
  (subsets of `input_tokens`; absent or `null` reads unknown), and
  `report --cost` rolls them into a top-level `cached_input_tokens` scalar with
  totals unchanged. The spend gate can now price comparable runs from usage
  records the operator hands over with `VERDICT_USAGE=<file|dir>`, in a published
  schema (schema, model, tokens, optional USD, source, window), with the
  self-reported `cost.json` as the fallback; a handed record joins by its window
  bracketing the run, and a named path that is missing or malformed refuses typed
  before the bundle is written. Automatic discovery of other tools' transcripts
  is gone — the optional converters live outside the binary in
  `examples/usage-adapters/`. With nothing handed over, output is byte-identical.
- 0ab8a01: Isolated local reviews. `fetch-local --worktree` checks the review target into
  a temporary detached worktree and builds the bundle from that checkout instead
  of the live tree, so caller tracing and verification run against real, stable
  code without disturbing the working tree. The checkout is always removed
  before the verb exits (success, refusal, or cancel); when no checkout can be
  created the verb refuses typed `worktree-failed` and writes nothing. Without
  the flag every byte behaves as before.
- b21eaff: Small additive reads. `post.json` and `record.json` now record `tool_version`,
  `prompts_hash`, and `model`, and `calibrate` and `trend` surface them so a
  mixed-version history reads as mixed. `show --json` gains a `data.fix` block
  beside the unchanged `data.item`: full sig, status, severity, deduped files,
  locator, suggestion, a `verify_hint` read from the bundle, and a fixed
  untrusted-evidence caution — a pure read with no flags, no execution, and no
  store writes, and missing history reads as null rather than invented text.
  `doctor` human output opens with a run stamp and closes with one line,
  `Summary: healthy|unhealthy — N ok, M warn, K fail`, while `--json` envelopes
  and exits stay byte-identical. Builds with no release metadata now say
  `0.0.0-unknown` everywhere — `--version`, the skill payload and receipt, and
  the posted or reported footer — instead of the footer saying `unknown`.
- b21eaff: Failures a 0.3.1 user could hit are gone. `fetch` on GitLab looks up the
  project id with the plain `owner%2Frepo` path — the old request carried a
  trailing encoded newline, which could fail the lookup at the first step.
  Provider lists are read completely: Forgejo files and reviews, GitLab MR notes,
  and Azure change entries and threads page instead of stopping at the first
  page, and a page cap, a failed page, or a failed thread read lands an envelope
  warning instead of silently under-covering. `fetch` retries GitHub's transient
  "internal error, please try again" on the same three-attempt budget `post`
  already had, while auth and validation failures still fail fast. Machine mode
  never prompts, even on a TTY: `init --json` and `install --json` without
  `--yes`/`--force` refuse typed `needs-confirm`, write nothing, and print the
  hint instead of waiting on a `Proceed?` prompt. Malformed agent input fails
  closed with `bad-review-input` — unparsable `review.json`, non-object
  `comments`/`triage`/`sections` entries, non-string scalars, misshapen
  verification claims — and a corrupt findings store now refuses typed
  `corrupt-store` across `report`, `show`, `calibrate`, and `trend` instead of
  crashing mid-render with no envelope, never starting fresh over history.

  A value flag given without its value now fails as a usage error naming what
  it wanted (`--provider needs a value (want <name>)`, exit 2) before any
  network call, instead of falling back to a default target. A bare `--stash`
  still means `stash@{0}`.

  Cancellation is clean. SIGTERM now cancels a running verb the way Ctrl-C
  does: exit 3 with a `canceled` envelope, and child process groups are killed
  instead of orphaned. A cancelled verb returns promptly even when a grandchild
  process still holds its output pipe. Interrupting `verify` refuses
  `canceled` and records nothing, where it used to record a false
  `timeout after 300s` proof and exit 0. The `mcp` stdio server takes the same
  signal: SIGTERM cancels an in-flight tool call and kills its process tree
  instead of orphaning a running `verify` for the rest of the verify budget.
  On Windows, `verdict upgrade` stages its
  helper beside the installed binary, so an install directory on another drive
  no longer makes the final move fail after the upgrade reported success.
  On Windows, `verify` starts its command suspended and puts it in its
  kill-on-close job object before it runs, so a grandchild spawned in the first
  instant can no longer escape a timeout or cancel kill.
  `threads.json` always labels a thread's author kind, including a thread with
  no readable comments, as the published schema requires. A cancel that lands
  inside a read verb's service (`ready`, `trend`, `threads`, `calibrate`,
  `check-tree`, `doctor`, `report`, `next`) exits 3 with the `canceled` envelope
  like every other verb, where it used to exit 1 as an ordinary refusal.

  Provider page walks finish what they start. A GitLab merge request whose diff
  list spans host pages (`glab api --paginate` concatenates one JSON array per
  page) now yields every changed file instead of a `PR.json` with no files at
  all, and a merged Bitbucket pull request (`state: MERGED`) folds to `merged`
  rather than `unknown`. A GitCafe review/comment cursor walk that stops early — a
  failed page, an unreadable page, or the 20-page cap — keeps the pages it already
  read, warns, and marks `threads.json` `truncated: true` instead of quietly
  under-covering the conversation; Forgejo's review list does the same at the
  provider cap. Hosts with no failures read exactly as before.
  A failed or unreadable Bitbucket diffstat/comments page, a GitLab changed-file
  payload that is not a diff list, and a failed Azure iteration read no longer
  pass as an empty list: each warns, and the Bitbucket threads feed is marked
  `truncated: true`.

  Report artifacts survive a kill. `report --output`, `report --sarif`, and the
  MCP report artifact are staged beside the target and renamed into place, so a
  crash mid-write can no longer leave a truncated report behind. A symlinked
  `--output` path keeps the link and replaces only the file it names, an existing
  file keeps its own mode (a new one is still owner-only), and a device like
  `/dev/stdout` still receives the bytes directly. A failed write refuses
  `report-failed` and leaves the previous report intact.

  Numbers in machine output render the way jq 1.8 renders them. A literal such as
  `9.223372036854775808e18` now prints as `9223372036854775808` on every
  architecture, where arm64 used to saturate at `9223372036854775807` and amd64
  wrap to `-9223372036854775808`.

  `verdict mcp install` no longer corrupts a Codex `config.toml` that uses a TOML
  multi-line string: a `[mcp_servers.verdict]` header spelled inside a `'''` or
  `"""` literal used to be merged into as if it were real, and the merge now
  refuses `mcp-config-unmergeable` (file untouched, add the table by hand)
  instead of guessing.

- b21eaff: Reviews of other repositories stop carrying this repository's own conventions.
  The shared system prompt now defers branch, commit-message, formatting, and
  style rules to the target repo's `AGENTS.md`/`CLAUDE.md`/`CONTRIBUTING`, with
  verdict's own rules staying in its `AGENTS.md`, so reviews of this repo keep
  them. The fix flow opens with the untrusted-data preamble, the abort-if-not-
  introduced pin is stated where the flow can follow it, and the manual records
  the machine-modes-never-prompt doctrine and a combined
  `report --check --completeness` pre-push probe. `--show-prompts` wraps every
  untrusted bundle section in a per-run nonce fence named in a trusted prologue,
  so a forged close smuggled in a diff stays evidence instead of ending the
  section; thread files stay off the initial-review reading list by design.
  Missing trusted prompt files no longer vanish silently: a missing
  `prompts/system.md` refuses typed, while a missing `SKILL.md`,
  `prompts/review.md`, or `prompts/diagrams.md` warns and proceeds; missing
  bundle files still skip silently.
- b21eaff: Credential and untrusted-input hardening. Every credential-bearing provider
  read is bound to the configured origin: `fetch` refuses a Bitbucket pagination
  `next`, a Forgejo metadata `diff_url`, or an Azure metadata `remoteUrl` whose
  scheme and host differ from the API base, instead of sending the token to that
  host, and Bitbucket paging stops at a page cap or a repeated cursor with a
  warning that the bundle may be incomplete. Command-stderr sidecars written by
  `fetch` and `post` (`.diff.err`, `.post.err`, `.recheck.err`, provider `.err`)
  are now owner-only `0600`, since they can carry token-adjacent forge
  diagnostics. Untrusted input is capped: `record` and `post` refuse an oversized
  agent file while reading it instead of after allocating the whole document,
  `verify` and the fetch runners cap captured child output (a capped proof
  records a truncation marker instead of growing the process), and every store
  reader rejects oversized documents while keeping its own refusal code. A review
  anchor, knowledge-base counter, override threshold, adopt anchor, verify
  timeout, or read number that does not fit in an integer now takes its typed
  refusal instead of wrapping into a nonsense negative span or count.

  The `verify` gate now refuses a command it cannot fully scan. A destructive
  tool name hidden inside `$(…)` substitutions nested deeper than the gate's
  scan bound, or inside a substitution body glued directly to a word (for
  example `$( (echo r))m -rf dir`), was previously treated as clean. Both now
  refuse before anything runs. A pull request file name containing a tab or
  newline can no longer forge or shift rows in `hunks.tsv`, and the ordered
  JSON reader rejects trailing bytes after the document (`{"a":1}}`) instead of
  silently ignoring them.

## 0.3.1

### Patch Changes

- 045744b: Basis-line validation compares the agent's line against the review's own comments instead of the merged findings store, so re-reviews with carried-over findings no longer warn. Genuine miscounts still warn and keep the body.
- 0c323cb: Same-head reposts omit the review link in the footer instead of pointing at the superseded review. Fresh posts keep the link; plain-text fallback unchanged.

## 0.3.0

### Minor Changes

- e8fa26c: Binding APPROVE now rechecks live open threads before posting: unresolved live threads the local store doesn't know about refuse APPROVE with a typed error (triage or `--force`); an unreachable lookup degrades to COMMENT with the reason stated instead of approving blind. COMMENT, dry-run, and local flows make no new network calls.
- 928fe5b: Attribution v2: the footer default is now a linked, leveled, logo-bearing line — a product-linked 18px logo (defaulting to the tracked `apps/docs/public/logo.svg` raw asset) plus `Reviewed with [**Verdict {version}**](https://stainless-code.com/verdict)` with the version inline space-separated, followed by the receipt fields. `publish.attribution` migrates from the day-old P1 bool to an `off`/`product`/`full` enum (file bools refuse with the mapping named: `"off"` for false, `"product"` for true; `full` appends `by [Stainless Code](https://stainless-code.com)`). New `publish.logo` (default true) and `publish.logo_url` knobs (empty reads text-only, never a broken image); a `<!-- verdict-footer -->` machine divider separates content from footer with strip-and-replace re-rendering; `attribution_text` gains the `{version}`/`{tier}`/`{product_url}` inventory with unknown placeholders refused.
- 401176b: Opt-in behavioral diagram: agent-drawn mermaid under a ported contract (`prompts/diagrams.md` with the 7-type selection map, honesty rule, and copy-exact skeletons), gated by a new `publish.diagrams` namespace (`behavioral` default off, `structural` default on for the P3 topology half, `expanded` default collapsed), grammar-level warn-and-drop validation that never fails a review, and `--diagram-behavioral/--diagram-structural/--diagram-expanded` flag pairs.
- 7e46a2e: Bounded verify-gated fix loops: new `fix` verb runs one pass per invocation over local bundles (top open inline sig or `--sig`), printing finding + full `kb.txt` context with patch → verification.json → record → re-run instructions. Hard-coded ceilings (max 3 passes per sig in `.review/<bundle>/fix.json`, scope-escape on out-of-anchor worktree touches), start gates (conflicted tree, closed bundle), terminal completion summary, local-only in v1.
- aa2ca37: Self-grading probe: `calibrate --probe` grades recorded findings against triage dispositions as the script-owned baseline, and merges agent judgments (`--judge`/`--judge-file`, validated, atomic, sticky) that may diverge — the agreement/dissent rate over doubly-judged rows is the instrument reading. Additive grade file only; findings, joins, and calibration rows untouched.
- 76135b5: Anchors validate against the fetch-time snapshot: `fetch` pins the commentable set and `post`/`record`/`adopt` reuse it (rebuilding only if missing, with a warning); dropped anchors ship as named body notes with reasons instead of failing or misplacing silently. Mid-run pushes read as drift context, not a moving validation target.
- 561e316: Structured local knowledge base: `learn` verb (append/list/retire/restore scoped rules with provenance, rigor, hit counters), `kb.txt` recall onto relevant diffs as context-never-orders, flip-time credit with false-hit-rate auto-retirement, and per-credit audit rows.
- 6cd6af8: Knowledge base: re-posting a finding without `kb` keeps the prior cite while explicit `"kb": []` clears it (uncite); every `.review/kb-joins.jsonl` audit row now also carries `bundle` and `review_id`.
- 7ea467f: Overlap audit: `trend --overlap` joins recall manifests × findings cites × the joins ledger into recall precision, recall coverage of cites, and the cited-outcome split, with joins corroboration and calibrate eligibility. Pure read, no network; missing manifests read as unmeasured, never zero.
- 05efe51: Change overview: `report --output` renders a script-computed collapsed file table plus a `flowchart TD`
  change-topology diagram on large diffs (more than 8 files or 24KB, `--overview`/`--no-overview` override),
  with stable F-row ids, deterministic relations only, and a new `publish.overview` knob.
- 106b40c: Permalink-first evidence: findings reference GitHub blob links pinned to the reviewed SHA (reference-style,
  `path:line` locators with definitions), with quotes over three lines kept inside collapsed detail blocks
  only. Non-GitHub bundles degrade to plain `path:line` text. Link validation warns and keeps, never failing
  a review over garnish.
- 9bcedfb: Debug legibility: `fetch`/`fetch-local --show-prompts` dumps the exact assembled prompt (system + mode + recall + coverage manifest) without running an agent; `report --output` bodies lead with confidence plus safety and list up to seven checked files on clean reviews, with full reasoning staying in the machine envelope.
- 55a5a27: Publish footer: posted bodies and `report --output` end with a merged receipt block (tool version, tier, cost or unrecorded, files reviewed, review link when known) including a factual one-sentence attribution, plus a deterministic confidence-basis line (rigor counts, cited rules, verified fixes). New `publish` settings namespace (footer/attribution/attribution_text/copyright) with flags beating file beating env; agent Basis lines validate warn-and-keep, never failing a review.
- 84e7c28: Recall manifest: the recall step emits `kb-recall.json` (recalled rule ids in rank order with scores) beside `kb.txt`, rendered from the same single sort. The human note is byte-identical, silence on clean is preserved, and all readers tolerate a missing manifest.
- 056099a: Report gate answers "is HEAD reviewed?": new opt-in `--completeness` flag on `report` (alone or with `--check <tier>`) compares the fetched HEAD against the completed-post record, failing closed with a typed error naming the re-fetch command. Without the flag, output and exits are unchanged.
- 1ec07ca: Onboarding pair: `review.sh init` scaffolds the standard local control files (budget, panel, KB policy, KB starter) with chill/standard/strict presets and runs check-tree validation, offline with preview-then-confirm; an XDG-homed presentation-only settings file (`output`/`layout`/`color`, schema-hinted, credentials refused) ends env sprawl with flags winning over file over env and byte-identical behavior when absent.
- 6674504: Operator-owned template overrides: per-block fragments under `.review/overrides/` (preamble, footer
  order, walkthrough threshold, attribution) guarded by an enumerated locked-invariant list (marker line,
  findings structure, guard satisfiability, metadata presence, divider shape), validated at check-tree time
  and revalidated at post time with typed refusals. `check-tree --json` emits the standard envelope on all
  paths.
- 53a4bbe: Exactly one terminal write, enforced: `post` refuses a second completed post on the same reviewed SHA with a typed error (`--force` overrides, new commits re-arm automatically). Dry runs, attempts, and failures never arm the guard. The existing exact-payload replay guard stays as the fine layer underneath.
- e304108: Copy-based skill installer: new `review.sh install` verb detects the de-facto agent skill dirs (.agents/skills, .claude/skills, .codex/skills at the repo root and $HOME), previews the copy plan for the tool-owned verdict/ payload (SKILL.md + prompts/), and applies only on explicit confirm (same preview-then-confirm idiom as init, typed needs-confirm). Only installer-owned verdict/ dirs (with a .verdict-managed receipt) are ever written — foreign skills and unowned dirs stay byte-identical; re-runs are idempotent and receipt/tool version skew warns instead of failing. Offline, shellcheck-clean, operator docs updated.

### Patch Changes

- c8b81f3: Bird's-eye review follow-ups: indented fenced blocks (up to 3 columns) now validate, single-line
  fences never open an end-of-body swallow, drop-notes insert before a carried footer divider so
  strip-replace preserves them, the doc wrapper is a parseable multi-line block, and the manual
  documents the full v1 placeholder inventory.
- 54cfcff: Pre-commit runs `shellcheck -S warning` over staged shell files (missing binary skips with
  a notice, same degrade-gracefully idiom as the formatter check), and the CI lint job covers
  every tracked `*.sh` instead of three hardcoded paths. Pinned by a hermetic hook eval
  (violate fails, clean passes, missing binary skips).

## 0.2.0

### Minor Changes

- 332d20b: Overridable fetch diff cap: REVIEW_MAX_BYTES env (or .review/max_bytes file) overrides the 512000-byte default for deliberate exceptions; refusal names the override last and the envelope carries cap provenance when set.
- 5341cde: Cost control plane: operator spend budget (env/file), pre-run estimate from comparable past runs, and consent flag for projected-over-budget runs with receipts in the cost rollup. Unset preserves today's behavior exactly.
- dd0b17b: Record effort tiers (cheap/standard/deep) on cost runs with per-tier rollup, default delta re-reviews to cheap lenses, and emit a sensitive-path manifest plus no-quote rule on fetch (advisory warning, evidence untouched).
- f96d7f5: Add a `ready` readiness-advisory verb (CI state, open threads, draft/conflict, description completeness — advice, never a gate) and per-review cost accounting (agent-reported run records plus `report --cost` rollup).

### Patch Changes

- dd0b17b: Robustness bundle: optional review-level `confidence` (1–5) and `security_summary` recorded on `record.json`/`post.json` (absent stays absent, out-of-range warns and drops); exact re-entry commands on post/record failures plus an echo-only interrupt trap on post; `error:`/`try:` failure-message pass with received values and valid ranges; fetch-local warnings for untracked/binary exclusions and empty paths filters; explicit fetch byte cap (512000 bytes) with split-or-narrow hint and distinct empty-vs-huge signals.
- dd0b17b: Add `ready --check`: opt-in readiness gating with machine exits (0 ready, 1 blockers-or-wait, 2 usage, 3 tool error). Default stays advisory with exit 0; hook recipe in the operator manual.
- 5341cde: Add `report --check <critical|important|informational>`: opt-in severity gating with machine exits (0 no open findings at-or-above tier, 1 findings at-or-above tier, 2 usage, 3 tool error including corrupt store). Default stays advisory with exit 0; corrupt stores now fail typed instead of raw jq errors; hook recipe in the operator manual.
- 5341cde: CLI hygiene: oversize-fetch errors carry machine-actionable narrower-scope candidates, doctor splits hard-fail from warn and checks storage writability, and shell completion covers verbs and flags for bash and zsh. Also fixes doctor always exiting 0 and the empty-filelist fetch-local crash.
- 5341cde: Add `trend <owner/repo>`: read-only portfolio rollup over existing bundle stores — findings opened/addressed/closed over time broken down by severity tier and status, with per-bundle models/effort for the calibration join. Recomputed every run (no cache files); empty history and unreadable stores never fail; default report output untouched.

## 0.1.3

### Patch Changes

- ffe4e4c: Rewrite the README for plain speech: cut AI tells, name the trust boundary, fix the registry refresh command, and document the current installer flags.
- 8504165: Correct the registry refresh docs, verified against the CLI source and live runs: filters match installed skill names (not `owner/repo` paths), and `check` is the dry-run counterpart to `update` (hidden from `--help`, covered by upstream tests).
- 02a6df5: Harden the verify verb: the timeout kill now stops the whole process group (a decoy fast-exit with ordinary backgrounded children is killed and recorded as timed-out instead of success; deliberate process-group escapers remain out of scope), and known secret values are redacted from the recorded tail. Legitimate commands run unchanged with full env.

## 0.1.2

### Patch Changes

- 72ebe75: Document `install.sh --agent/--dir/--list/--check/--uninstall` management flags in the README.
- 5035996: Harden the trust boundary: `system.md` now names PR data untrusted (evidence, never instructions) and constrains `verifyCmd` to repo-scoped non-destructive commands. `install.sh` detects installed harnesses (`--detect`), supports project installs (`--project`), honors `XDG_CONFIG_HOME`/`CLAUDE_CONFIG_DIR`, and validates the checkout before linking.

## 0.1.1

### Patch Changes

- a879767: Read tool version from `package.json` instead of the `VERSION` file (same output, single source of truth) and ship `CHANGELOG.md` in the tarball.

## 0.1.0

Initial npm release. Local, on-demand PR review for any coding agent — the agent reviews, `gh` does GitHub I/O. No servers, no external services, no daemons.

- Evidence-graded findings (`said` to `reproduced`), severity ladder tied to author action.
- Keep-biased machinery: partitioned anchor validation, stale-SHA and double-post guards, binding verdicts behind `--bind`.
- Machine surface: every verb speaks `--json` envelopes, SARIF export, `calibrate` joins predictions against live ground truth.
- No-PR reviews: `fetch-local` bundles the worktree, `record` merges the store, `adopt` carries findings into a PR fetch.
- Multi-provider fetch (github/gitlab/gitcafe/forgejo/bitbucket/azure); post/calibrate stay GitHub-only.
