# @stainless-code/verdict

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
