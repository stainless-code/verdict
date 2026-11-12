# Verdict — operator manual

Verdict splits code review into steps your agent runs on your machine: fetch,
review, gate, post. The agent reviews; `gh` does GitHub I/O. No servers, no external services.

## Quickstart

```bash
make build   # writes bin/verdict; or use the installed copy
export GH_TOKEN="$(gh auth token)"   # or a PAT with contents:read, pull-requests:write, issues:write

# 0. onboard once per repo (optional, offline): scaffold .verdict/state/ controls
verdict init --preset standard --yes

# 1. fetch
verdict fetch owner/repo 123

# 2. review with ANY agent (the installed skill is the prompt; add it with
#    `verdict skill install --detect` if it is missing):
#    Point the agent at .verdict/state/123/. It reads .verdict/state/123/full.diff fully via
#    .verdict/state/123/toc.txt, loads prompts/review.md from its skill dir, and fans out
#    0-N lenses per prompts/reviewer-subagent.md for disposition-changing questions only.
#    Write exactly one .verdict/state/123/review.json {schema_version: 1, body, approved, comments[], resolved?, triage?, sections?}.

# 3. post (exactly once — validates anchors, merges findings, submits)
verdict post owner/repo 123

# 4. read the report (offline)
verdict report owner/repo 123
```

New to the vocabulary? The [glossary](glossary.md) defines lenses, panel,
evidence rungs, keep-biased, and its sibling doctrines.

`review.json` schema:

```json
{
  "schema_version": "1",
  "body": "> [!IMPORTANT]\n> ...\n\n**Reviewed changes**\n...\n<!-- metadata -->",
  "approved": false,
  "comments": [
    {
      "path": "src/api.ts",
      "line": 42,
      "side": "RIGHT",
      "body": "2-3 sentences, actionable.",
      "rigor": "walked",
      "severity": "important",
      "in-bounds": true
    },
    {
      "path": "src/api.ts",
      "start_line": 42,
      "line": 44,
      "side": "RIGHT",
      "body": "…",
      "suggestion": "    fixed code…\n",
      "brief": {
        "sites": [
          { "path": "src/api.ts", "line": 42 },
          { "path": "src/queue.ts" }
        ],
        "outcome": "Reject duplicate deliveries before anything is enqueued.",
        "approach": "Track delivery ids in a bounded LRU checked at the top of the handler.",
        "questions": ["Should the dedupe window be 24 hours or 7 days?"]
      }
    }
  ],
  "resolved": ["a1b2c3d4"],
  "triage": [
    {
      "sig": "e5f6…",
      "status": "wont-fix",
      "rationale": "By design: … (≥30 chars)"
    }
  ],
  "sections": [
    {
      "heading": "### ⚠️ Rollout needs a migration window",
      "severity": "important"
    }
  ]
}
```

Inline rules: `path` must be in the fetched diff (a comment on any other path
refuses), `line` = NEW-side line inside an `@@` hunk (`side` RIGHT default,
LEFT for deleted lines; any other side refuses), multi-line needs `start_line`
in the same hunk, `suggestion` must preserve exact indentation. An absent
`line`/`start_line` warns and moves the comment to the body; a present
non-numeric anchor warns and keeps it inline. A fenced `suggestion` warns and
posts as plain body text. Suggestions render as fenced blocks in `body` (raw
keys never hit the API). `brief` is the optional machine form of the same
finding: `outcome` (required, 10–500 chars) plus `sites` (≤10 `{path, line?}`,
each path a changed file and each line inside a reviewed hunk), `approach`
(10–1,000), and `questions` (≤10, 10–300 each). Brief shape, type,
required-field, and range failures refuse the review; a site outside every
hunk, a site/question past its cap, or a short approach/question warns and
drops only that part. `brief` is outside the sig, so editing it never creates
a finding.
`resolved` lists finding sigs (short ids from the prior `findings.json` table) the run addresses.
`sections` carries severity tags `{heading, severity}` for `###` body sections — exact heading string, unmatched tags ignored.

The commentable set is pinned at fetch time: `fetch`/`fetch-local` writes
`.verdict/state/<bundle>/hunks.tsv` (per-file, side-aware `NEW`/`OLD` ranges from
the fetched diff) and `post`/`record` validate anchors against that pinned
snapshot. A push that lands after fetch only adds drift context (the
stale-`HEAD` note) — it never moves the validation target. Anchors outside
the snapshot move into the body under `### ℹ️ Unanchored observations`,
each named with its path/line and reason (e.g. `anchor 50-50 (RIGHT)
outside reviewed hunks`); drops never fail the run. If the snapshot is
missing, the run rebuilds it from `full.diff` with a best-effort warning —
re-fetch to pin it.

## Findings store + triage authority

`post` writes `.verdict/state/N/findings.json`: one record per inline comment and body `###`
section with a stable line-number-free signature, `status` (`open`/`addressed`/`wont-fix`/
`false-positive`/`deferred`), first/last seen SHAs, evidence rung (`said`/`pointed`/`walked`/
`ran`/`reproduced`, default `pointed`), the optional `brief` (`sites`/`outcome`/`approach`/
`questions`, stored additively — a finding without one is written without the
field), rationale for terminal states, and append-only `history`.
Incremental runs dedup by sig — recorded findings are not re-reported unless the delta changes them. Before posting, `post`
prints a fail-closed preflight block (body verdict, anchor counts, finding counts, hunk index).
Binding verdicts need `--bind` (else downgraded to `COMMENT`); `APPROVE` is refused while
`open` + `deferred` findings stand (`--force` overrides). On the binding `APPROVE`
path only, `post` re-lists live unresolved threads before resolving the verdict
and refuses `APPROVE` when more threads are open live than the store holds open
(`approve-blocked`, naming the counts — fetch + triage the unknowns, or re-run
with `--force`). An unreachable recheck downgrades to `COMMENT` with the reason
in the body and warnings — never a silent `APPROVE`, never a hard failure.
`COMMENT`, `REQUEST_CHANGES`, dry runs, and local bundles never pay this lookup.

Two dedup layers keep "exactly one terminal write" mechanical: the fine guard
refuses exact payload replays (same body+comments+SHA hash, dot-marker
`.posted_<hash>` — legit re-reviews hash differently and pass), while the
coarse single-review finalizer refuses any second completed post on the same
reviewed SHA — even with different content — with typed `already-posted`
(pass `--force` to override the wedged case). Any new commit SHA re-arms
automatically (the next post refreshes the `post.json` head); attempts, dry
runs, and failures never write the key, so only a real post arms it.

Finalizer loop (one shape end to end — refuse → advance → delta review →
post → gate): the `already-posted` refusal names both exits — `--force` to
override, or the forward path `verdict fetch owner/repo 123
--base-sha <short-posted-head>` (the 7-char reviewed SHA) and review the
delta per `prompts/incremental-review.md` step 2. A push that lands after
fetch never fails the run: `post` warns on stderr and appends the `> ⚠️`
body note, both naming `verdict fetch owner/repo 123
--base-sha <short-fetched-sha>` — copy-paste it, review the delta, post,
then close the loop with `report --completeness` (stale-head on a bundle
with a posted head names the `--base-sha` advance form; missing-post with
no head names the plain `fetch`). A binding `APPROVE` requested against a
known stale fetch is downgraded to `COMMENT` with the reason (the review
does not cover live HEAD — re-fetch the delta to bind; `--force` does not
override this, only a fresh delta binds). A lookup that
cannot reach the network warns and proceeds — the nudge never refuses.

Authority rule: **the script disposes, the agent proposes, the human vetoes.**
Bare `resolved[]` sigs record a claim but never flip status. `addressed` requires
a `.verdict/state/N/verification.json` claim (`{sig, verdict: VERIFIED, verifyCmd, resultHead}`
with `resultHead` matching HEAD). The operator or agent writes that claim —
`verify` writes `patch.json`, not `verification.json`; a claim is attestation,
not tool proof. `record`/`post` cross-check the bundle's `patch.json` when
present and warn on a nonzero exit or timeout, but the flip stands; inspect
`patch.json` for the tool's machine result. `NOT VERIFIED` reopens,
`INCONCLUSIVE` only logs. `triage[]` claims other states with rationale (unknown enum or
unknown sig refused/ignored; short rationale keeps prior status). The operator mutates the
store directly between runs with `verdict triage … --sig … --status … --rationale …`
(human supremacy, every override history-logged).

Malformed agent input refuses typed (exit 1, machine envelope with `error.code`,
nothing written — no `findings.json`/`record.json`, no KB credit). `record`
reports every review-input problem at once as `bad-review-input` with
`error.problems[]`; `post` keeps `bad-review-schema` for a non-object document.
Misshapen `verification.json` claims fail closed as `bad-review-input`;
a present-but-corrupt (or non-list) `findings.json` refuses as `corrupt-store` —
the run never starts fresh, since that would silently lose history (restore from
backup, then re-run). The read verbs apply the same guard to every entry:
`report`/`show`/`calibrate` refuse `corrupt-store` on non-object entries or
non-string `sig`/`body`/`heading`/`kind`/`suggestion` (and `report` on an
unreadable `patch.json`), instead of dying mid-render with no envelope;
`trend` warns and skips corrupt bundles, rolling up the readable ones, so one
bad bundle never fails the whole audit. An unreadable `source.json` head degrades to the dirty-head
path instead (`record.json` head reads `"dirty"`).

### Review-input problems

`record` and `record --dry-run` run the same pure checker over `review.json`
and `full.diff` (`pkg/verdict.CheckReview`). The record path also hands it the
chunk manifest names and `verification.json` claims, so comment/brief parts,
anchors and sides, kb ids, chunk attestation, confidence, security summaries,
claims, triage, sections, and verification shapes all reach one list. It walks
the document in order, collects every problem with its JSONPath and exact fix,
caps the list at 50 (`error.problems_truncated` counts the overflow), and
refuses once as `bad-review-input`. A clean dry run exits 0, writes nothing,
and reports `data.check` — comment, section, and moved-anchor counts. Human
mode prints every problem before the `try:`/`re-enter:` lines; JSON mode
carries the same list in `error.problems[]`. A warning alone exits 0 and rides
`warnings`; `--strict`, or `"strict_input": true` in the bundle's
`controls.json`, promotes every warning to an error. Level is each code's
typical level; `mixed` means the level depends on whether `record` can drop or
move the part (warning) or must refuse it (error). The published code set
(also the `problem.code` enum in `envelope.schema.json`). `record --dry-run`
validates local and provider-PR bundles and writes nothing; a real `record` on a
PR bundle still refuses `not-local` (use `post`).

Before checking, `record` runs `NormalizeReview` over the agent's bytes: one
whole-document code fence is unwrapped, prose around exactly one balanced JSON
object is dropped, a scalar where a string array belongs becomes a one-element
array, the aliases `inBounds`/`rung`/`file` become `in-bounds`/`rigor`/`path`,
rigor synonyms and enum case are folded, and a BOM, an explicit null on an
optional key, or an exact-duplicate comment is removed. Every rewrite is a
warning with its own code and is listed in `record.json`'s `repairs[]`; strict
input turns each into an error. A repair never invents or rewrites prose, and
an unparsable or non-object document, a comment with no recoverable string
`path`/`body`, an unknown `schema_version`, a corrupt store, or partial chunk
coverage still refuses. `record --dry-run` adds `data.normalized` (the exact
object the checker read) and `data.repairs`; verdict never rewrites
`review.json` on disk. The CLI also owns the body's `> Basis:` line: it
replaces a disagreeing line, or inserts a missing one, with the counts computed
from the review and reports `basis-replaced`.

| Code                   | Level   | Meaning                                                                                                  |
| ---------------------- | ------- | -------------------------------------------------------------------------------------------------------- |
| `invalid-json`         | error   | The file is not one JSON document; the message carries line:col, and the fix is to emit only the object. |
| `wrong-type`           | mixed   | Error for a wrong type; warning for a non-numeric line/start_line or a fenced suggestion.                |
| `missing-field`        | mixed   | Error for a required field; warning for missing schema_version, comments, or line/start_line.            |
| `bad-enum`             | mixed   | Error for an unknown closed-set value; warning for a malformed KB id or unknown chunk name.              |
| `out-of-range`         | error   | A length or numeric bound is outside its published range (confidence, brief approach/question, caps).    |
| `path-not-in-diff`     | mixed   | Error for a comment path; warning for a brief site outside the diff.                                     |
| `unknown-field`        | warning | A key is not in the published shape; the fix names the nearest known key.                                |
| `anchor-outside-hunk`  | warning | The line is provably outside every hunk; `record` moves the comment into the body.                       |
| `rigor-unset`          | warning | A comment carries no evidence rung; it records as `said` with `rigor_source: default`.                   |
| `normalized-fence`     | warning | One whole-document code fence was unwrapped.                                                             |
| `normalized-extracted` | warning | Prose around exactly one balanced JSON object was dropped.                                               |
| `scalar-to-array`      | warning | A scalar where a string array belongs was wrapped in a one-element array.                                |
| `key-alias`            | warning | A known alias (`inBounds`, `rung`, `file`) was renamed to its canonical key.                             |
| `enum-synonym`         | warning | A rigor synonym or a case-folded enum value was rewritten to the published value.                        |
| `normalized-trivia`    | warning | A BOM, an explicit null on an optional key, or an exact-duplicate comment was removed.                   |
| `basis-replaced`       | warning | The body's `> Basis:` line disagreed with the computed counts, or was missing, and the CLI replaced it.  |

### Sig contract (sig_version 1)

The sig is the cross-run join key — findings, deltas, calibration, and any external
policy/gating tool all join on exact sig equality. Derivation: `sigKey` is
`path + "\n" + heading + "\n" + body` (inline findings use `path`, section findings
use `heading`); normalize by squashing every whitespace run (spaces, tabs, newlines)
to one space and trimming ends; take the lowercase hex SHA-1. Line numbers, sides,
severity, and rigor are deliberately excluded, so findings survive line shifts and
re-worded metadata. Every entry stamps `sig_version: "1"`; merges backfill it onto
older entries produced by the same normalization. Merges also backfill `rigor_source`
onto older entries from the recorded rung: `pointed` reads as `default` because the
previous merge filled a missing rung with `pointed`, and every other recognized rung
reads as `model`. A review that explicitly wrote `pointed` is indistinguishable from
a default-filled one, so those entries read as `default` either way. Changing the normalization orphans
history — that ships as a new `sig_version`, never silently.
`review.json` inputs stamp `schema_version: "1"` (`post`/`record` refuse unknown
versions, warn-and-proceed when the stamp is missing); every bundle carries
`.verdict/state/N/store.json` (`{"schema_version":"1"}`) marking the generation of the
whole store — plugins read it instead of sniffing shapes. GitHub bundles keep
`PR.json`/`reviews.json`/`inline.json` as raw API output (GitHub's schema,
unversioned); the other hosts reshape `reviews.json`/`inline.json` per host
and normalize `PR.json` to one schema. Every provider bundle also writes
`threads.json` (`schema_version: "1"`, published as
[`schemas/threads.schema.json`](https://github.com/stainless-code/verdict/blob/main/skills/verdict/schemas/threads.schema.json)): the raw
files stay as-is, and the feed is the authoritative PR conversation. Review
threads come from each host's native source — GitHub GraphQL `reviewThreads`
(REST has no resolution), GitLab discussions (`resolved`/`resolvable`), Azure
thread `status`, Forgejo review-comment `resolver`/position, Bitbucket
`resolution` + `inline.outdated`, GitCafe's comment flags. Each thread carries
`id`, `path`/`line`/`start_line`/`side`, `resolved`/`outdated`, `url` when
cheap, root `author`/`author_kind` (`human|bot|verdict`), optional
`finding_sig`, `kind`, and `comments[]` (`id`, `author`, `author_kind`, body
capped at 8 KiB with a `truncated` flag, `created_at`). Top level: `provider`,
`pr`, `head_sha`, `fetched_at`, `truncated`. `verdict threads owner/repo N
[--provider …]` re-reads only the thread surface and rewrites only this file,
so a re-review after pushing fixes sees current resolution state.
`verdict threads resolve owner/repo N [--thread <id>]... [--dry-run]` closes
self-authored threads: a thread is resolvable only when its root carries
verdict's marker and its root author equals the authenticated token user the
run posts as (read once per run; an unreadable login refuses before any
write). Without `--thread`, every open self-authored thread whose linked
finding the local store marks addressed; a forged-marker thread skips as
`not-self-authored`. With `--thread`, only the named ids, which must still be
self-authored (a human or forged thread refuses `not-verdict-authored`).
`--dry-run` lists the plan and makes zero writes; a real run makes one host
write per thread, marks them resolved in `threads.json`, and writes
`threads-resolve.json` (provider, repo, PR, head, timestamp, `resolved[]`,
`skipped[]` with reasons). GitHub and GitLab carry the mutation; Forgejo
refuses typed because its API has no documented resolve endpoint.
`post.json` and `record.json` carry `schema_version: "1"`.

### Bundle namespace (core files vs `x-*`)

A bundle dir holds these core/script artifacts (`PR.json`/`source.json`,
`full.diff`, `toc.txt`, `roles.txt`, `review.json`, `findings.json`, `findings.dryrun.json`, `record.json`,
`post.json`, `store.json`, `hunks.tsv`, `reviews.json`, `inline.json`, `threads.json`,
`threads-resolve.json`,
`panel`, `patch.json`, `verification.json`, `incremental.diff`, `kb.txt`,
`lenses.txt`, `sensitive.txt`, `kb-recall.json`, `cost.json`, `budget.json`, `fix.json`,
`kb-grades.jsonl`, `controls.json`, `controls/`, `chunks/`
(`chunk-NN.diff`, `chunk-NN.toc.txt`, `chunk-NN.roles.txt`, `manifest.json`
with `schema_version: "1"`, per-chunk `<name>.cost.json`), plus dot-scratch
`.difffiles`/`.chunk-hunks.tsv`/`.posted_*`/`.compare.err`/`.diff.err`/`.post.err`/`.recheck.err`).
Dot-scratch `.err` sidecars are owner-only (`0600`) debug residue, not published
bundle content. Router-authorized
agent terminal writes (`report.md` via `prompts/ci.md`, `replies.json` via
`prompts/address-reviews.md`, `noreview.md` via `prompts/incremental-review.md`)
are core-tolerated scratch: core verbs never read them. Anything else a
plugin or future verb adds MUST live under `x-*` (`x-*.json` recommended) — core
verbs never read or write `x-*`, so plugins can't collide with core files and
`check-tree` (which ignores all of `.verdict/state/`) never trips on them. Agents write
only `review.json` (plus `verification.json`/`patch.json`/`report.md`/`replies.json`/`noreview.md`
where a mode file says so);
everything else in a bundle dir is script-owned.

`plan.txt` is not a bundle file: the combined plan is captured in `run.json`,
and `plan.txt` is the fence label for its rendered section in the
`--show-prompts` dump, one line per group (see
[Policy engine](#policy-engine-scopes-profiles-deep-validation)).

### Lens packs

A lens pack is data, not a verb: `pack.json` names each lens and
`lenses/<id>.md` carries its body. The built-in pack ships at
`skills/verdict/packs/builtin/` (`name` `verdict/builtin`, `version` `1.0.0`)
and `fetch`/`fetch-local` load it from the embedded skill payload. The format
is strict JSON (unknown fields refuse), versioned by `schema_version: "1"`,
and published as `schemas/pack.schema.json`. The fixed layout is `pack.json`,
`lenses/` (loaded), and the reserved `rules/`, `examples/`, `evals/` (counted
by the caps, not loaded); a file no manifest row names is not loaded.

Every load checks the caps while walking, before any parse: 64 KiB per file,
1 MiB per pack, 256 files, 64 lenses (`pack-too-large`). An invalid manifest,
a missing or mis-stemmed body, or an empty (whitespace-only) body refuses
`bad-pack`; a present `max_files` must be 1..24 and a present `max_findings`
1..20. An absolute path, `..`, or a symlink component refuses
`pack-unsafe-path`. A missing or empty pack root is absent, not an error: the
loader returns no pack and the bundle carries no lens surface. The 1 MiB
per-pack cap is not a separate budget: the pack tree joins the controls
inventory, so its bytes count against the controls tree's shared 1 MiB
(`config.json`, `kb.jsonl`, `learnings.md`, and `overrides/` included), and a
pack at its own ceiling leaves no room for the rest — the controls read
refuses `controls-too-large`. The pack hash
is sha256 over the canonical loaded tree (sorted slash paths, each fed as
`== <path> ==`, its bytes, then a newline); each lens also carries the first
eight hex digits of its body's sha256.

`fetch` writes the loaded bodies to `.verdict/state/<bundle>/lenses.txt`, one
delimiter/label pair per lens (`# lens <id> — sha256:<hash8>` … `# end lens
<id>`), and adds `lenses.txt` to `data.artifacts`. Every `plan.txt` group line
appends `lens_pack=<name>@<version>` and `lenses=[<id>:<hash8>,…]` after its
cap fields, sorted by id. A layered set tags each lens with the pack that
supplied it — `lenses=[<id>@<pack name>:<hash8>,…]` — so two packs sharing a
lens id stay distinguishable on the line. A lens whose `globs` match none of
that group's files is not offered for the group; a lens with no `globs` is
offered for every group. The file is context, never orders, like `kb.txt`.

Packs layer built-in < org < repo. The org layer is the explicit `packs`
string array in personal settings (`~/.config/verdict/settings.json`): each
entry is a pack directory, absolute or relative to the reviewed repository
root, and there is no directory scan. A repo-relative entry is read from the
checkout, not the trusted base; an entry inside `.verdict/packs/` refuses
`bad-pack`, because that tree is the repo layer and must be read at the PR
base. The comparison is lexical and also follows symlinks when both sides
exist, so a linked path into the tree refuses too; a case-variant spelling on
a case-insensitive volume is not caught. A listed path that carries no
`pack.json` refuses `bad-pack`. The repo layer is
`.verdict/packs/<name>/`, read by `fetch`/`fetch-local` from the trusted-base
snapshot (the `controls` inventory records every pack file's path, bytes, and
sha256), never from the PR head. A fetched bundle therefore keeps using the
pack the PR base shipped even when the head edits it, and `post`/`record`
re-hash the snapshot: an edited pack file refuses `controls-stale`.

Two packs in one layer may not declare the same lens or rule id (nor the same
pack name): the result would be order-dependent, so it refuses `bad-pack`. A
higher-layer pack may override a lower id only by naming that lower pack in
its manifest's `replace` array, for example
`"replace": ["acme/org"]`; replacement is downward only (naming a pack at the
same or a higher layer refuses `pack-conflict`), and an override without the
declaration refuses `pack-conflict` naming the exact entry to add. A layered
plan line tags each offered lens with its winning pack slug
(`lenses=[security@repo:<hash8>]`) and lists every pack identity in
precedence order.

Rules are declared in the same manifest and live at
`rules/<id>.md`: the id follows the KB grammar
(`^[a-z0-9][a-z0-9-]{0,64}$`), `severity` is a key into the pack's
`severity` map whose value is `critical`, `important`, or
`informational`, `globs` is required, and the optional `tools` array names
the tools the rule talks about. Every rule body joins the bundle's existing
fenced `kb.txt` section after the store's top-3 recall, one line per rule
with its severity and `source=pack:<name>@<version>`; `kb-recall.json`
mirrors those two fields machine-readably. Rules never enter the trusted
prompt, and the shipped prompt hash does not move.

`verdict pack list [--json]` reports every loaded pack (built-in, org, repo)
in precedence order: `name`, `version`, `layer`, `source` (`builtin`,
`dir:<path>`, or `.verdict/packs/<name>`), the canonical tree `hash`, the
declared `lenses` and `rules` ids, and `drift` (null until `verdict.lock`
lands). It lists the packs in this checkout; a review reads repo packs from
the PR's base, so a pack the PR adds or edits is listed here but not used.
A regular file directly under `.verdict/packs/` (a README, say) is not a
pack: `pack list` skips it with one warning naming it, and `fetch` ignores
it, so a stray regular file never refuses the run. A symlink or special file
under `.verdict/packs/`, or a `.verdict/packs` that is not a real directory,
refuses naming the joined path — `pack list` as `pack-unsafe-path`, `fetch`
as `controls-unavailable` — so neither surface calls one ignorable. Conflict
refusals surface here too, so a bad layering fails before a review spends
anything. The MCP `pack` tool serves the same read-only list and repeats the
same warning list in its result.

### Evidence ledger

Seven artifacts explain what a run knew and what it proved. Each row states
the artifact, who writes it, and whether a verb gates on it.

| Artifact                  | What it is                                                                                                                                                                                                                                                             | Who writes it                                                                                                                 | Gates?                                                                                                                                                                                                                                                                                                                                 |
| ------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `run.json`                | The typed per-run capture: resolved defaults, per-file resolutions, plan, spend and privacy views, trusted-base inventory hashes, base SHA, the `inputs_sha256` digest, and appended check observations.                                                               | `fetch`/`fetch-local`; `post` appends observations                                                                            | Yes, except `config --preview` (see below). `post`, `record`, `triage`, `report`, `ready`, `config --explain`, and `verify --group` verify it; a run bundle deleted while its controls snapshot remains, or an edited captured input, refuses `controls-stale`; a bare bundle dir reads with a warning and no repository requirements. |
| `validation-<group>.json` | The deep-validation receipt for one plan group: run, inputs digest, group, reviewed and execution heads, canonical root, command, exit, timeout and sandbox flags, status, and a redacted output tail.                                                                 | `verify --group <id> --command <cmd>`                                                                                         | Yes. `post` refuses a binding approval while any `depth: "deep"` group lacks a current receipt; `ready` reports the gap as `profile-work`.                                                                                                                                                                                             |
| `filesource.json`         | Provenance for the PR file list: `source` (`paged` or `cli`), `cli_cap` (100), `provider_ceiling` (3000), `count`, and `truncated` or `paging_error`.                                                                                                                  | GitHub `fetch`, when the CLI list arrives at its 100-file cap                                                                 | No. It is an advisory coverage note: a paging failure warns and the run proceeds with the CLI's first 100 files.                                                                                                                                                                                                                       |
| `full.raw.diff`           | The raw pre-exclusion diff, kept as the patch-identity input when a filtered diff would otherwise replace it. No shipped verb writes it today: `fetch` writes the host diff to `full.diff` and records the filtered hash in `run.json` without writing filtered bytes. | Nothing today (a future exclusion pass)                                                                                       | Partly. When present it feeds the identity, and `identity.json`'s `raw_diff_sha256` must reproduce it; a present-but-unreadable file refuses instead of falling back to `full.diff`. Absence is normal.                                                                                                                                |
| `sensitive.txt`           | The advisory manifest of secret-adjacent paths: filename, extension, and added-line content hits, one path and reason per line, behind a comment header.                                                                                                               | `fetch`/`fetch-local` (and every provider fetch)                                                                              | No. It is advisory, not a gate: flagged paths stay reviewable evidence, the file is removed when nothing is flagged, and its header asks reviewers to name the path, never the value.                                                                                                                                                  |
| `incremental.diff.note`   | One `incrementalUnavailable: <host stderr head>` line explaining why the GitHub or GitLab compare read produced no delta.                                                                                                                                              | GitHub/GitLab `fetch --base-sha/--since` on a failed compare, or any other provider on the flag (naming the unsupported host) | No. No verb reads it; it explains the absent `incremental.diff` beside it while the full-diff review proceeds.                                                                                                                                                                                                                         |
| `plan.txt`                | The combined plan as the `--show-prompts` dump renders it, fenced under this label: one line per group with its stable id, profile, depth, `max_lenses`, checks, files, scopes, panel, panel source, effort, per-group cap, lens pack, and offered lenses.             | `fetch`/`fetch-local --show-prompts`                                                                                          | No. It is a rendered view for the agent; `run.json` holds the captured plan every gate reads.                                                                                                                                                                                                                                          |

`config --preview` is the exception: when `run.json` is absent it can fall back
to its file list, though it still verifies `controls.json` and may lose
captured rename identities.

## Learnings (durable repo memory)

`.verdict/learnings.md` at the repo root is the skill's only cross-run memory: durable
knowledge (test commands, conventions, gotchas) in `##`-headed sections. `fetch` reports
its base-resolved copy (`.verdict/state/N/controls/learnings.md`, see
[Review controls are base-owned](#review-controls-are-base-owned)); agents read headings
first and challenge stale claims. Post-run reflection appends durable lessons (never
transcripts) to the worktree file — commit it, then re-fetch; do not run `learn` or edit
the file while a review is in flight. Deleting a wrong entry beats editing around it.

## Knowledge base (scoped local rules)

`.verdict/kb.jsonl` at the repo root is the structured layer above `learnings.md`:
one JSON object per line — `{id, rule, scope, provenance, rigor, hits,
false_hits, judged, status}` (`id` matches `^[a-z0-9][a-z0-9-]{0,64}$`,
`scope` is a non-empty comma-separated glob list, `rigor` is
`said`/`pointed`/`walked`/`ran`/`reproduced`, counters are non-negative ints
with `judged == hits+false_hits`, `status` is `active`/`retired`). Commit it and
it compounds, in a repo whose `.gitignore` admits the file. `.verdict/state/` is run state:
this checkout ignores the whole directory, so no review state is committed here.
A repo that carries its controls in the base commit negates them in its own
`.gitignore` instead, where `git add` can see them.

`learn` manages it (no repo/PR args, no network):

```bash
verdict learn --rule "Check error paths on every await" --scope "src/*.ts" --provenance manual --rigor walked
verdict learn --status            # rules + counters + precision
verdict learn --retire <id> --reason "<30+ chars, on the record>"
verdict learn --restore <id> --reason "<30+ chars, on the record>"
```

`--id` is optional (slugged from the rule text when absent). Exact-duplicate
rule text and taken ids are refused; empty rule/scope and bad rigor/id fail
typed. Retire keeps the tombstone line (`status`, `retiredAt`,
`retireReason`, history-logged) with counters untouched; restore flips back to
`active` keeping the tombstone as record.

Recall: `fetch`/`fetch-local` writes `.verdict/state/<bundle>/kb.txt` when active
rules scope-match the diff — top 3 by precision (`judged==0` reads 0.5),
tiebreak judged desc then id. Agents read it as context, never orders
(contract line in `prompts/review.md`); clean diffs leave no file, and with no
`kb.jsonl` nothing changes at all.

The recall manifest `.verdict/state/<bundle>/kb-recall.json` mirrors the note
machine-readably: a JSON array in rank order, each entry
`{id, score, hits, judged}` where `score` is the ranker's `0.5` for unjudged
rules else `hits/judged`. It is written together with `kb.txt` (clean trees
leave neither file) and never changes `kb.txt` bytes; readers tolerate its
absence, so bundles fetched before the manifest still read fine.

Credit: flipping a finding to `addressed` credits cited rules `hits+1`, to
`false-positive` credits `false_hits+1` (`judged+1` either way) — staged with
the flip, then committed right after the findings file persists
(history-stamped `kb_credit: [{rule, to}]`, one credit per rule per finding
per outcome, deduped across adopted twins by the joins log), dry-runs credit
nothing, `calibrate` never writes counters. Per-finding `kb` arrays cap at 5
ids (extras warn-and-drop). Re-posting a finding without a `kb` key keeps its
prior cite; `"kb": []` clears it (uncite); a malformed `kb` warns, drops to
`[]`, and likewise clears. Every credit appends a
`.verdict/kb-joins.jsonl` audit row carrying `bundle` (the bundle dir name)
and `review_id` (the posted review id, null when no post.json exists yet —
local bundles and unposted PRs).

Retirement is data-driven: after any credit, a rule with `judged` at/above
`REVIEW_KB_MIN_JUDGED` (default 5) and `false_hits/judged` above
`REVIEW_KB_MAX_FP_RATE` (default 0.5) retires with a tombstone. Env wins, then
the typed `defaults.kb_policy` object in `.verdict/config.json` (global only —
inline scope or requirement policies refuse `bad-config`), then built-in
defaults; invalid values warn and fall back, never failing the flip. Retired
rules never recall; citing one warns and drops.

## Review controls are base-owned

A pull request must not be able to edit the rules it is judged by. `fetch` and
`fetch-local` therefore read every review control from the PR base commit —
`git show <base>:<path>` through the command seam, never the worktree — and
snapshot the bytes into the bundle:

| Control                                    | Consumed by                           |
| ------------------------------------------ | ------------------------------------- |
| `.verdict/config.json` (typed defaults)    | `fetch` panel, budget, cap, chunking  |
| `.verdict/kb.jsonl`                        | `fetch` recall; `record`/`post` cites |
| `.verdict/learnings.md`                    | the review agent (headings)           |
| `.verdict/overrides/*.md` (four fragments) | `record`/`post` body and footer       |
| `.verdict/packs/**` (every regular file)   | `fetch` lens and rule layers          |

Set strict input with `REVIEW_STRICT_INPUT=1`; every `fetch`/`fetch-local`
resolves it into `controls.json` (do not hand-edit that snapshot) and it makes
`record` refuse on review-input warnings, the same as `--strict`.
`.verdict/state/N/controls/` holds the bytes and
`.verdict/state/N/controls.json` records
`policy` (`base`, or `head` under the opt-out), `base_sha`, the commit `sha` the
files were read at, and each file's `path`, `status` (`used`/`missing`), `bytes`,
and `sha256` (schema: `schemas/controls.schema.json`). The inventory includes
every regular file under `.verdict/packs/`, one row per file, so the pack tree
is hashed exactly like the rest of the controls; a symlink or special file in
that tree refuses `controls-unavailable` instead of being copied. The base is `PR.json`'s base
commit for `fetch`, and the mode base for `fetch-local` (`HEAD`, the merge-base for
`--base`, the range base, the stash parent), so uncommitted edits to a control are
not read. Environment knobs (`REVIEW_PANEL`, `REVIEW_MAX_BYTES`, `REVIEW_BUDGET_USD`,
`REVIEW_KB_*`) still win over the file layer. A default run prints nothing extra;
the JSON envelope gains `data.controls` (`policy`, `sha`, `files`).

`post` and `record` load `controls.json` before any merge read and re-hash every
used file. They refuse:

- `controls-unavailable` — no or invalid `controls.json` (re-fetch), or at fetch
  time the base commit is not readable: a GitHub `fetch` falls back to the contents API
  (`gh api repos/<owner>/<repo>/contents/<path>?ref=<base>`, a 404 reading as an absent
  control) when the checkout lacks the base, but any other API failure refuses, and
  other providers refuse outright (use `actions/checkout` with `fetch-depth: 0`, or
  `git fetch --depth=1 origin <base>`; verdict never fetches for you), or a control is a
  symlink.
- `controls-stale` — the snapshot's base is not the bundle's base, or a file's
  bytes changed under the bundle: re-fetch.
- `controls-too-large` — a control over 256 KiB (1 MiB across all); never truncated.

`--allow-head-controls` on `fetch`/`fetch-local` is the only opt-out: it reads the
worktree as before, warns once (human and JSON), and stamps `policy: "head"`.
Track your controls once so the base commit carries them — in a repo whose
`.gitignore` admits them, `git add .verdict/config.json .verdict/kb.jsonl
.verdict/learnings.md` (and any other control you use), then commit.

## Typed repository configuration

`.verdict/config.json` is the only repository configuration file:
one strict document (schema `skills/verdict/schemas/config.schema.json`) for
defaults (`panel`, `budget_usd`, `max_bytes`, `chunk`, `kb_policy`, `effort`,
the two severity floors), the local `index` caps (`files`, `file_bytes`,
`tree_bytes`, `shards_bytes`, `keep_trees`, `refs_per_file`), blocking
clauses, path scopes, named profiles, and the bind requirement. `fetch`/`fetch-local` read it from the base commit (or
the reviewed worktree for local runs) through the same trusted read; unknown
fields, misplaced nulls, credential keys, and unsatisfiable clauses refuse
typed (`bad-config`, `bad-config-credentials`, `bad-requirement`, `bad-scope`)
before any provider call. Layer order is flags, environment
(`REVIEW_BUDGET_USD` and friends), repository scopes and shared defaults,
personal review settings, built-ins. The personal layer lives in the operator
settings file and carries only a spend ceiling, privacy exclusions, and review
defaults — it can never raise the repository budget, and its exclusions are
enforced before any model-facing export while the raw bundle keeps every byte.

Each fetch persists a run bundle (`.verdict/state/<pr>/` or
`.verdict/state/local-<slug>/`) with the allowlisted fetch-time evidence and
the combined pending plan. `post` enforces the stored block clauses and
`require_bind` against the full findings (below-floor findings still gate even
when they stay out of the payload), `report` renders below-floor findings out
of the view while gates keep the full set, and every check execution appends
an observation to the bundle. `verdict config <owner/repo> <pr> --explain`
renders the stored policy; `--preview [--file <path>]` resolves a proposed
file or the captured inventory with current settings and environment. Without
a capture, repository inputs are reported as unavailable. Explain and preview
perform no live calls and no writes, and never approve; the human explain
summarizes values, requirements, plan groups, spend, privacy, and checks, while
`--json` additionally carries shadowed values and per-file resolutions, which
is the debugger surface. Resolving the root runs
one `git rev-parse` probe through the environment rather than an injected
commander. The full rule table lives in
`docs/policy-contract.md`.

## Policy engine (scopes, profiles, deep validation)

`fetch`/`fetch-local` resolve the repository policy once and persist it in
`run.json`; the review verbs (`post`, `record`, `triage`, `report`, `ready`,
`verify --group`) read that capture rather than the live
`.verdict/config.json`, so an edited capture refuses `controls-stale` instead
of silently changing the rules. Layers resolve in order: flags, the
`REVIEW_*` environment, matching path scopes, shared `defaults`, the personal
settings layer, then built-ins.

### Path scopes and profiles

A `scopes[]` entry is `{name, include, exclude, profile, defaults,
requirements}`. A scope matches a reviewed path when one `include` glob
matches and no `exclude` glob does; a rename matches on the old path, the new
path, or both. Every matching scope refines the defaults field-by-field with
the later entry winning, and the last matching scope that names a `profile`
wins (a repository-wide `default_profile` applies when none do). A scope can
also carry inline `defaults`, so per-path `panel`, `budget_usd`, `max_bytes`,
`chunk`, `effort`, and the two severity floors need no shared default. A scope
needs one of `profile`, `defaults`, or `requirements`; naming both a `profile`
and inline `defaults` refuses `bad-scope`, while `profile` plus
`requirements` is legal.

A `profiles` entry is `{depth, checks, max_lenses}`. Depth is `standard` or
`deep`; checks are the typed guards the run must satisfy
(`no-open-above-block`, `head-current`, `no-live-threads`); `max_lenses` caps
the lenses a group may dispatch (`0` means none, absent means uncapped).
Files that resolve to identical effective defaults, requirements, and profile
become one plan group — the unit every later verb names. `plan.txt` renders
each group as one line:

```text
- <label>: id=<stable id> profile=<name> depth=<standard|deep> max_lenses=<N|unset> checks=[...] files=[...] scopes=[...] panel=[...] panel_from=<source> effort=<tier> cap_bytes=<n> cap_from=<source> chunk=<bool>
```

The group's `panel` (first entry synthesizes, the rest staff lenses) overrides
the shared roster, `effort` is that group's accounting tier, and `max_lenses`
is enforced as attestation, not observation: a capped group must carry its
dispatched labels in the usage record bound to the capture (`cost.json`), and
a group that record omits stays unknown rather than zero.

### Deep validation

`depth: "deep"` means "standard review plus a recorded validation pass". The
pass is operator-supplied proof run by the tool:

```bash
verdict verify owner/repo 123 --group <stable-id> --command "<project proof command>"
```

Both flags are required: the service enforces the `verify --group <id>
--command <cmd>` pair, so `--group` without `--command` (or the reverse)
refuses `bad-group-validation`, and neither keeps the patch `verify` path. The
bundle must carry a verified `run.json` (`no-run-bundle`) and the id must name
a plan group (`unknown-group`). Before anything runs, the tool pins three
facts: the canonical reviewed root, the bundle's reviewed head, and the
checked-out head, which must be that same commit (otherwise `head-mismatch`).

The command runs once through the same allowlist and watchdog as patch
verify, and every outcome leaves a receipt — a failure is never dropped.
Add `--sandbox` to either form to run the command inside the enforced Linux
`bwrap` sandbox; the receipt then records `"sandboxed": true`. The
receipt is `<bundle>/validation-<group>.json` (group ids are lowercase
`[a-z0-9-]`, at most 64 chars, so the path stays confined), read back through
a 64 KiB cap and written atomically. It records the run name, `inputs_sha256`, group id,
reviewed and execution heads, canonical repo root, command, timestamp, exit
code, timeout and sandbox flags, `status` (`passed`, `failed`, `timeout`, `unavailable`,
`stale-head`), and a redacted output tail. Reads re-derive the verdict: a
receipt claiming a pass while recording a nonzero exit, a timeout, or another
status is contradictory evidence and never approves. A head that moves during
the command records `stale-head`; a capture re-fetched or edited mid-command
records `unavailable`.

`post` reads the receipts at approval time. A binding approval
(`--approve=true --bind`) refuses `requirement-unmet` until every deep group
has a current receipt for this run, inputs digest, group, head, and root;
`--force` and consent are never consulted. `ready` and `report` show a
missing or stale receipt as required work without executing anything.

### Readiness blockers

`ready` reports live blockers, each with a recovery action:

| Kind                  | Blocker                                                                                |
| --------------------- | -------------------------------------------------------------------------------------- |
| `ci-failing`          | N failing checks (the failing names are listed).                                       |
| `threads-open`        | N unresolved review threads.                                                           |
| `draft`               | The PR is still a draft.                                                               |
| `conflict`            | `mergeable=CONFLICTING` or `mergeStateStatus=DIRTY`.                                   |
| `description-thin`    | The description is under ~50 chars.                                                    |
| `compliance`          | A stored finding violates an applicable block clause.                                  |
| `compliance-conflict` | A required review withholds a reviewed path by an exclusion rule.                      |
| `compliance-unknown`  | Captured provenance is unavailable, so compliance is unproven.                         |
| `profile-work`        | A deep group lacks a current receipt, or a capped group lacks usable lens attestation. |
| `compliance-pending`  | A required check has no passing observation for the saved head.                        |

`ready` stays advisory (exit 0); `ready --check` turns the same list into the
machine exits 0 ready, 1 blockers-or-wait, 2 usage, 3 tool error.

On GitLab the same blockers read through `glab`: the merge request carries
`draft`, `has_conflicts` (plus a `conflict` `detailed_merge_status`), and the
`description`, while CI comes from the merge request's pipeline list (a failed
or canceled pipeline is one failing check, falling back to the head pipeline
status when the list is empty) and threads come from the discussions list.

### Severity floors

Two typed floors decide what is shown and what is published:

- `display_min_severity` withholds below-floor findings from the rendered
  `report` view. Gates still read the full ranked set, so a floor never hides
  a finding a gate would fail on.
- `post_min_severity` withholds below-floor findings from the published
  payload. Block clauses, compliance, and the approval guards still read the
  full stored findings, so a floor mutes what the review says; it never
  weakens a gate.

Both floors resolve per finding path — a matching scope's own floor outranks
the shared default — and compare rank, `critical` > `important` >
`informational`. Missing and unknown severities are never withheld, because
the vocabulary cannot place them. `post.json` records what the floor did:
`post_min_severity` when a global floor applied, `below_floor_excluded` for
the withheld count, and `post_floor_scoped` for each path-scoped floor that
actually withheld a finding (`[{"path": "vendor/lib.go", "floor":
"critical"}]`). Omitted keys mean no floor applied and nothing was withheld.
`post.json` also records what the _host_ withheld — see § Providers below.

### Diff coverage (agent attestation)

A non-chunked bundle has no built-in evidence that the agent read the diff it
reviewed. `review.json` may carry an attestation, and verdict checks it against
the bundle's own hunk snapshot:

```json
"coverage": [
  {"file": "src/db.ts", "ranges": "1-240,300-320"},
  {"file": "src/app.ts", "ranges": "all"},
  {"file": "cmd/verdict/main.go"}
]
```

`file` is a repo-relative path that must name a changed file with hunks.
`ranges` is optional; absent or `"all"` attests the whole file. Otherwise it is
a comma-separated list of 1-based inclusive source-line intervals, and it is
side-agnostic — the evaluator unions the file's `OLD` and `NEW` hunk intervals,
so one interval covers a hunk on either side. Duplicate `file` entries union
rather than count twice. A malformed range warns and makes that entry count as
unattested; it never silently attests the file.

Coverage is computed from `hunks.tsv`, the snapshot `fetch` already writes —
never from a second diff parse and never from `toc.txt` line counts. Paths the
shared role heuristic classifies as generated or vendored are exempt
automatically; no sentinel is required. `percent` is `null`, never `0`, when no
changed line was measured.

The default posture is a warning plus one machine-built body line on a partial
attestation. A partial one warns once and adds one machine-built body line;
`coverage: []` ("I attest nothing") stays distinguishable from "no attestation
was written", and a review that never stamped the key gets neither the warning
nor the line, so its rendered body is unchanged.

`gate` reads the stored receipt by default: a gated HEAD with no receipt, one
carrying no `coverage[]` attestation, or one measuring under 100%, fails the
merge gate (`--coverage warn` reports the same shortfall without failing). That is the only place the requirement is on
by default — `post` and `record` keep the warning posture below.

`--require-coverage` on `post` and `record` turns absence or a gap into a
`coverage-incomplete` refusal that names the unattested ranges. `--force` does
not bypass it: this is a read receipt, not a security boundary, but a require
flag a force flag can ignore is not a contract. The refusal is raised before
any store write, so a refused run leaves the prior `findings.json` untouched —
including `post --dry-run`, which refuses identically and writes no dry-run
store. `next <owner/repo> <pr> --coverage` reads the same numbers with no
`findings.json`, so the gaps are visible before recording; it exits `0` on a
partial attestation because it is a nudge, not the gate.

`post.json` and `record.json` carry the measured summary when an attestation was
present: `{"percent": 82, "attested": 18, "total": 22, "files": 3, "exempt": 1,
"gaps": 2}`, where `gaps` counts unattested ranges (not lines).

A chunked bundle is not evaluated line by line: `chunks[]` is its attestation
and the existing `missing-chunk-coverage` refusal is its merge-time gate.
`coverage[]` on a chunked review is ignored with one warning. Do not translate
chunks into ranges or the reverse. A chunked `post.json`/`record.json` carries
its own receipt instead of the measured summary — `"chunk_coverage":
{"attested": 1, "total": 2}` — counting the manifest chunks `chunks[]`
claimed, keyed to the same head. A claim the manifest cannot name never counts,
and a review with no `chunks[]` key stores `0`, so the receipt never attests
more than was written.

## Machine surface

`fetch`/`post`/`triage`/`report`/`gate`/`next`/`show`/`verify`/`doctor`/`calibrate`/`fetch-local`/`run`/`record`/`adopt`/`fix`/`check-tree`/`ready`/`threads`/`trend`/`index`/`backtest`/`learn`/`pack`/`init`/`skill`/`upgrade`/`uninstall` take `--json`: stdout is exactly one JSON envelope
(`schema_version`, `verb`, `repo`, `pr`, `at`, `next`, `warnings`, `data`, `error`),
all human progress goes to stderr, failures emit `{data: null, error: {code, message}}`
non-zero. `mcp config` also takes `--json`. Machine modes never prompt: under `--json` (and any future `--agent` plain-text mode) the script never blocks on interactive setup or confirm — it fails typed (exit 1 — or 2/3 on usage/gate-tool errors — with a `try:` hint) or degrades with a notice, so agents run it headless. `threads` refreshes only `.verdict/state/<pr>/threads.json` from the host (all six
hosts; GitHub reads GraphQL `reviewThreads`) and returns the feed tally — it
never refetches the diff, so it is the cheap re-read after replies,
resolutions, or a push. `report` is the pure offline reader over the store
(ranked items, verdict, counts, patch passthrough, optional `--output` markdown
with machine-header markers,
optional `--sarif` for scanners/CI). `--output` markdown leads with
`> Confidence: … · Safety: …` (from the stored `post.json`/`record.json`;
`unrecorded`/`not assessed` when the run recorded neither) and, on clean
reports (no open findings), closes with a `## What we checked` list of at
most seven bundle files (plus an overflow pointer to `toc.txt`). Under the
lead, `--output` renders a `> Basis: …` line computed exactly from the
findings store (rigor rung counts in evidence order, distinct cited kb rule
ids, addressed findings as verified fixes — covering the whole store
regardless of display filters; the agent-authored `> Basis:` line in `review.json` is replaced with the counts computed from the review's own comments, so carried-over findings never warn). Every `--output` file and every posted body
ends with one merged footer block (tier, cost or unrecorded, files
reviewed, review link when known, plus a leveled attribution line with
logo) — see
Publish footer + attribution knobs. Item bodies
stay 200-char summaries; full reasoning lives in the `--json` envelope only.
Distinct from `prompts/ci.md`'s agent-written
`.verdict/state/N/report.md` (no-post CI loop, `SKILL.md` router row) — never point
`report --output` at that path; the verb's markdown carries `report-machine`
markers, the agent's does not. Severity is stored at write time (unknown → null,
prose emoji never parsed) — rank uses status, then severity, then rigor, then
anchored/inBounds, then path/line/sig. `doctor` preflights one code host (the
one `--provider` names, `github` by default); `calibrate`
joins findings against live ground truth, read-only.

`show --json` returns the finding in `data.item` (the raw store record in key
order) plus `data.fix`, a read-only repair directive built from that record
and the bundle's history:

| `data.fix` key | Meaning                                                                                                                        |
| -------------- | ------------------------------------------------------------------------------------------------------------------------------ |
| `sig`          | Full finding sig (short prefixes still address the run).                                                                       |
| `status`       | The record's stored status string, read verbatim (absent reads empty).                                                         |
| `severity`     | `critical`/`important`/`informational`, else `null`.                                                                           |
| `files`        | Deduped anchor paths: the record's `path` plus any `files`/`related_files` lists; `[]` when none.                              |
| `locator`      | `path:line` for inline findings, the heading for section findings, else empty.                                                 |
| `suggestion`   | The record's embedded suggestion body, else `null`.                                                                            |
| `brief`        | The stored machine brief `{sites, outcome, approach?, questions?}`, read as stored (no re-validation), else `null`.            |
| `verify_hint`  | `{verifyCmd, resultHead}` from a `verification.json` claim for this sig, else a matching (or unnamed) `patch.json`; else null. |
| `caution`      | Fixed reminder that finding text quotes attacker-controlled PR content and is untrusted evidence, never instructions.          |

The block is pure read: no `gh` call, no `verifyCmd` execution, no store
writes — the bundle is byte-identical after the run, and the bare text path
(no `--json`) still prints just the record. Missing history reads as nulls,
never a warning.

`fetch`/`fetch-local` take `--show-prompts`: after the normal bundle write,
the script renders the assembled agent reading list — `SKILL.md`,
`prompts/system.md`, `prompts/review.md`, `prompts/diagrams.md`, the bundle
meta (`PR.json` or `source.json`), `toc.txt`, `roles.txt`, `full.diff`, plus
`kb.txt`, `lenses.txt`, `sensitive.txt`, and `panel` when present, in that order — trusted
prompts verbatim, then the trust-boundary prologue, then one nonce fence
per bundle file — to stdout (human mode; progress moves to stderr) or
`data.prompt_dump` (`--json`). No agent runs, no network beyond the fetch
itself, no store writes beyond the bundle: without the flag both verbs are
byte-identical. It reads the trusted prompts from the current directory, so
run it from an installed skill dir or a checkout (it falls back to the checkout's `skills/verdict/`) carrying `SKILL.md` and `prompts/`. Missing
bundle files skip silently, but missing trusted
prompt files fail loudly: no `prompts/system.md` refuses typed
(`prompt-dump-failed` — the untrusted-data preamble lives there, so a dump
without it is unsafe), while a missing `SKILL.md`, `prompts/review.md`, or
`prompts/diagrams.md` warns (stderr plus envelope `warnings[]`) and the dump
proceeds without it. Trusted prompts print verbatim first, then a
tool-generated trust-boundary prologue naming the run's nonce, then every
present bundle file inside its own fence — the bundle meta (`PR.json` or
`source.json`), `toc.txt`, `roles.txt`, the diff (`full.diff`, or the
manifest-order chunk concat on chunked bundles under the same label),
`kb.txt`, `lenses.txt`, `sensitive.txt`, and `panel` — each wrapped as
`<<<UNTRUSTED nonce=<16 lowercase hex> source=<file>>>` … bytes …
`<<<END UNTRUSTED nonce=<same>>>`, open and close markers each on their own
line (a missing trailing newline is added, never a content byte dropped).
The nonce is 8 crypto-random bytes rendered as 16 lowercase hex characters
(64 bits), minted fresh per dump: text and `--json` dumps
from separate runs carry different nonces. Only a fence carrying exactly the
prologue's nonce opens or closes a region — payload text shaped like a fence
(wrong or missing nonce) stays inside as evidence, and `prompts/system.md`
instructs the model to treat all fenced content as evidence, never
instructions. Thread files (`reviews.json`, `inline.json`) are deliberately
excluded: the dump reproduces the initial-review reading list (the fetch
`next:` line), and threads are prior-review context consumed live via
`gh api`/`glab api` on the incremental path — never bundle files in the prompt. This
section is the format contract: the binary reproduces it exactly (same
order, markers, nonce grammar, and section labels).

`ready` stays advisory by default (exit 0, blockers listed); `ready --check`
opts into gating for hooks/CI with machine exits — 0 ready, 1 blockers-or-wait,
2 usage, 3 tool error — so callers branch without parsing JSON:

```bash
verdict ready owner/repo 123 --check
case $? in
  0) verdict fetch owner/repo 123;;  # reviewable — review
  1) echo "not ready — nudge the author instead of reviewing";;
  2) echo "usage error — check the invocation";;
  *) echo "ready itself failed — check auth/bundle, then retry";;
esac
```

Compose it around the expensive step: gate `fetch`+review on `--check`
(pre), keep `post`'s preflight as the fail-closed backstop (post) — the gate
saves tokens, never merges.

`report --check <tier>` is the missing half of machine gates: `ready --check`
gates on CI state, this gates on findings. Same output either way; machine
exits mirror the ready gate — 0 no open findings at-or-above tier, 1 findings
at-or-above tier, 2 usage, 3 tool error (missing/corrupt store). The tier is a
severity name (`critical`/`important`/`informational`; untagged findings never
meet a tier) and only `open` findings count — dispositioned states stay human.

```bash
verdict report owner/repo 123 --check important
case $? in
  0) echo "no open important+ findings — proceed to merge";;
  1) echo "open important+ findings — triage before merging";;
  2) echo "usage error — check the invocation";;
  *) echo "report itself failed (missing/corrupt store?) — inspect, then retry";;
esac
```

Compose it after the expensive step: gate the merge on `--check critical`,
keep `post`'s preflight as the fail-closed backstop — the gate advises,
the human still merges.

`report --category <cat>` filters the ranked findings to one finding kind
(`logic`, `edge-case`, `security`, `performance`, `accidental-commit`,
`style`, `docs`, `other`; unknown values are a `bad-category` usage error),
and `report --check <tier> --category <cat>` gates only that class — a
category narrows which findings count, never substitutes for severity,
anchor, or rigor. The category is agent-supplied evidence on each inline
comment (never inferred, never part of the finding's `sig`); absent and
`null` both read untagged in `report`, `calibrate`, and `trend`.

`report --completeness` answers a second question from the same read — "is
HEAD reviewed?" — by comparing the bundle's fetched HEAD against the local
completed-post record (`post.json`, written only by a real `post`):
a match passes; a missing, unparsable, head-less, or mismatched record
fails with a typed `missing-post`/`corrupt-post`/`stale-head` check line
naming the re-fetch command — the `--base-sha <short-posted-head>` advance
form when `post.json` carries a head, the plain `fetch` when there is
nothing to advance from (local bundles keep the `fetch-local` form). Alone it gates on HEAD-reviewed only;
combined (`--check critical --completeness`) it fails when tier-hits OR
stale/missing post do. Pure local reads, no network; without the flag
output and exits are exactly today's.

A third dimension runs last on every gate invocation (`--check`,
`--completeness`, or both): the script re-lists live unresolved threads
and fails the gate when more are open live than the store holds open
(`check: N live open thread(s) vs M local open finding(s) — gate fails
(fetch + triage them, then re-check)` — fetch the PR, triage the new
threads, re-run `report --check`. The probe reads the bundle's own host:
GitHub through `gh api graphql`, every other provider through the adapter
the `threads` refresh already uses — so a GitLab, GitCafe, Forgejo,
Bitbucket, or Azure bundle gates on its threads too. A read that fails, and
a read that stopped before the host's last page, both leave the surface
unread rather than clean: each warns (`live thread recheck unreachable`)
and, under the default `--live auto` posture, leaves the dimension neutral:
the gate reflects local dimensions only, and the run never exits 3 over the
network. Only local bundles skip the lookup with a warn (`live recheck
skipped: ...`), paying zero network; plain `report` without a gate flag
never calls the network. There is no `--force` on report: fetch + triage +
re-check is the recovery, and advisory `report` without `--check` is the
escape hatch.

Offline posture (`--live auto|strict|skip`, gate runs only) is always an
explicit operator choice — automation never inherits it silently. `auto`
(the default) keeps the fail-open behavior above: a dead network reads as
"unknown", not "clean", and the warn plus the envelope `warnings[]` entry
say so. `strict` fails the gate closed on an unreachable lookup (`check:
live thread recheck unreachable ... — gate fails: offline posture is
strict; restore the network or re-run with --live skip, then re-check`,
exit 1, still never 3 — the network is a gate condition, not a corrupt
store). `skip` pays zero network and gates on the local dimensions only,
saying so loudly: a `live recheck skipped (--live skip ...)` warn (also in
`--json` `warnings[]`) plus a neutral skipped `check:` line in the gate
tail. Structural skips stay neutral in every posture — a local bundle has
no live threads to verify, so there is no live dimension to pass or fail. A
partial or failed live read is not a structural skip: `strict` fails it
closed and `auto` reads it neutral, exactly like a dead network. Pick
`strict` for merge gates that must never report ready while threads stand
open live; pick `skip` for air-gapped runs where the operator accepts the
local-dimensions-only verdict on the record. `--live` without a gate flag
is inert: advisory runs never touch the network regardless.

### `gate`: one composed exit code for CI

`report --check` and `ready --check` answer one question each. `gate` asks
every gating question in one call and returns one CI exit code — the verb a
pipeline step uses when it wants "is this PR shippable" without composing the
checks and their error branches itself:

```bash
verdict gate owner/repo 123 --max-severity critical --require-head --live strict
case $? in
  0) echo "gate passed — every requested dimension is clean";;
  1) echo "gate failed — the gate: lines name the dimensions and the next action";;
  2) echo "usage error — check the invocation";;
  *) echo "gate itself failed (auth/bundle/store) — inspect, then retry";;
esac
```

At least one dimension is required: `--max-severity critical|important|
informational` (open findings at-or-above the tier; untagged findings never
meet a tier), `--require-head` (a completed post for the fetched HEAD, the
same record `report --completeness` reads), and `--live auto|strict|skip`
(unresolved threads on the PR; the dimension runs only when another dimension
asked for it, and `--live` alone is a usage error rather than a silent pass).
A dimension is `pass`, `fail`, `unknown` (unreadable and failing closed), or
`skip` (not requested, or unreachable under `auto`); only `fail` and `unknown`
fail the gate, so a skip never turns a green run red. Exit codes mirror
`report --check`: 0 pass, 1 fail-or-unknown, 2 usage, 3 tool error.

`--coverage require|warn` sets the posture of the coverage dimension, which
runs on every gate and defaults to `require`. `require` reads the stored
receipt `post.json` or `record.json` carries for the gated HEAD — the summary
[Diff coverage](#diff-coverage-agent-attestation) already describes — and fails
the gate when that receipt is absent, names another HEAD, carries no
`coverage[]` attestation (a completed post that never attested coverage reads
`unknown`, not `pass`), or measures under 100%. `warn` runs the same
evaluation, prints one `warn:` line, and marks the dimension `skip`, so the
shortfall is reported without failing — the migration for a review that has
not attested yet, not a permanent posture. A bundle whose changed files are
all exempt passes either way once a receipt attests them (`percent: null`,
`total: 0`).

A chunked bundle is graded on the chunk attestation its receipt stores:
every manifest chunk in `chunks[]` passes, and a shortfall fails naming the
chunks still unattested (`1 of 2 chunk(s) unattested`), because `chunks[]` is
that bundle's only attestation. A receipt written before that key existed
carries no chunk attestation, so it reads `unknown` under `require` and `skip`
under `warn` until the bundle is re-recorded or re-posted. Like `--live`, the
posture alone is not a dimension, so `--coverage` alone is still a usage error.
The gate reads the receipt and never re-evaluates `review.json`, so it cannot
publish a number the store does not hold.

The human tail is one line per dimension plus the verdict and a single
`try:` hint naming every failing dimension's recovery in order:

```text
gate: findings     fail    2 open finding(s) at-or-above critical
gate: live_threads fail    3 live open thread(s) vs 2 local open finding(s)
gate: FAIL (2 fail)
try: address or triage the open findings, then resolve or reply to the live
threads, then re-run gate
```

`--json` publishes the same verdict as `data.passed` plus `data.dimensions[]`
in `findings, head, coverage, live_threads, compliance` order (`id`, `status`,
`detail`, `count` —
null where a dimension has no tally). A failed gate is a verdict, not a
refusal: the envelope keeps `error: null` and the exit code carries the
outcome. `gate` is read-only — no store writes, no `post.json`, no review
submission — and it is not an MCP tool (the SDK and TS client reach it as a
method): a local pass is not authority to merge, so the merge stays the
human's call.

Compose it where `report --check --completeness` used to go; the flag set is
the same dimensions (plus the coverage receipt and the captured compliance
verdict), and the flagless form remains advisory `report`.

### MCP server (stdio, experimental until 1.0)

`verdict mcp` serves the review verbs as MCP tools over stdio: newline-delimited
JSON-RPC 2.0 on stdin/stdout, one frame per line. There is no daemon, listener,
HTTP/SSE mode, or remote auth: the process shares the host's credentials and
exits when stdin closes. It is listed as experimental in the usage block, and
`verdict mcp --help` documents it.

Tools are named after the verbs and take the core request shape: `adopt`,
`calibrate`, `check-tree`, `config`, `doctor`, `fetch`, `fetch-local`, `fix`, `init`,
`learn`, `next`, `post`, `ready`, `record`, `report`, `show`, `threads`,
`trend`, `verify`. That is 19 tools. `upgrade` and `skill` are distribution surfaces,
never tools; `triage` is human-owned and joins the surface only with
`--operator`, so an agent proposes status changes through `triage[]` in
`review.json` instead. Input schemas are generated from the Go request structs,
so they cannot drift from the core contract; `repo`/`pr` plus verb fields are
the arguments. Closed vocabularies and numeric bounds the structs declare
(`status`, `kind`, check tier, `preset`, `rigor`, `approve`, provider names)
become schema `enum`/`minimum`/`maximum` keywords, so a bad value is a schema
error the model sees before calling instead of a runtime refusal. `post` writes
a review and sends it to the host (GitHub, GitLab, or Forgejo), so it is not
exposed under `--read-only`.

`--read-only` exposes eight read tools (`report`, `next`, `show`, `trend`,
`ready`, `doctor`, `check-tree`, `config`) plus `record` limited to its `dry_run: true`
form, and is mutually exclusive with `--operator`. A non-dry `record` call
there is an `isError` `bad-flags` result naming `dry_run`; the tool keeps
`readOnlyHint: false` because the same tool writes by default. `report` writes
nothing there either: the surface drops its `output` and `sarif` arguments and
refuses them as unknown. `threads` is not exposed at all, because it rewrites
`.verdict/state/<pr>/threads.json` from the host; its `resolve` mode is
operator-only, because it closes host threads, and a server started without
`--operator` refuses that call naming the flag. Every review verb except the
human-owned `triage` is exposed by default because the host already authorizes
the binary and its credentials. A call to a tool the flags hide is a JSON-RPC
`-32602` error whose message and `data` name the tool and the flag combination
that exposes it (`post` under `--read-only`, `triage` without `--operator`), so
a hidden verb is never reported as an unknown one; a truly unknown name still
answers `unknown tool`.
`--operator` adds `triage` (20 tools) for an operator's own assistant that is
allowed to flip statuses, and it unlocks the `threads` resolve mode; it is
never the default. Each `tools/list` entry
carries a short `title` plus the four MCP behavior hints (`readOnlyHint`,
`destructiveHint`, `idempotentHint`, `openWorldHint`) derived from the verb's
real behavior: `post` is destructive and open-world, `fetch` is open-world,
`report` is idempotent and carries no `readOnlyHint` because `output`/`sarif`
write files, and `threads` carries none because it writes `threads.json` and
can resolve host threads.
Descriptions state what the tool does,
when to call it, and what it writes or sends. `initialize` returns an
`instructions` string with the same doctrine (fetch, review, post; one review;
refusals carry a `hint`; never `triage` without `--operator`), so an MCP-only
agent sees it before its first call. Each
`tools/call` returns the CLI `--json` envelope's `data` as one text block plus
`structuredContent`; a typed refusal sets `isError: true` with
`{code, message, hint}` in both places, plus `retry_at` when the refusal is
retryable. The text block is budgeted at 256 KiB:
a larger result keeps `structuredContent` whole and replaces the text with a
JSON summary carrying `truncated: true`, the measured `bytes` and `limit`, the
on-disk `artifact` (the bundle directory when the verb reports one) and a
`next` step, so one oversized `report` cannot silently consume the agent's
context. A malformed call is a JSON-RPC `-32602` error, not a refusal. Human
progress and warnings go to stderr (or MCP notifications), never stdout:
stdout is the protocol channel.

#### Host config

`verdict mcp config --agent <claude|cursor|codex|gemini|qwen|windsurf|opencode|goose|copilot>`
prints the exact entry (the default is print-only, like `--dry-run`). Add
`--read-only` or `--operator` (mutually exclusive, like `verdict mcp`) to put
the matching argv after `mcp` in the snippet. Add `--write` to merge it into the host's documented config, or
`--json` for one envelope with the path, format, snippet, and merge status. It
never prompts and never starts the server.

| Agent      | Config file                                            | Format | `--write`  |
| ---------- | ------------------------------------------------------ | ------ | ---------- |
| `claude`   | project `.mcp.json`                                    | JSON   | merges     |
| `cursor`   | project `.cursor/mcp.json`                             | JSON   | merges     |
| `codex`    | `$CODEX_HOME/config.toml`, else `~/.codex/config.toml` | TOML   | merges     |
| `gemini`   | `~/.gemini/settings.json`                              | JSON   | merges     |
| `qwen`     | `~/.qwen/settings.json`                                | JSON   | merges     |
| `windsurf` | `~/.codeium/windsurf/mcp_config.json`                  | JSON   | merges     |
| `opencode` | project `opencode.json`                                | JSON   | print-only |
| `copilot`  | project `.vscode/mcp.json`                             | JSON   | print-only |
| `goose`    | `~/.config/goose/config.yaml`                          | YAML   | print-only |

```json
{ "mcpServers": { "verdict": { "command": "verdict", "args": ["mcp"] } } }
```

```toml
[mcp_servers.verdict]
command = "verdict"
args = ["mcp"]
```

`--write` is idempotent: an existing matching entry is `keep` and nothing is
written; an absent one is `create`/`update`. An existing `verdict` entry whose
argv is one of verdict's own shapes (`["mcp"]` plus any combination of
`--read-only`/`--operator`) is updated in place to the requested argv; any
other command/argv refuses typed (`mcp-config-foreign`) instead of being
rewritten. The previous file is copied to `<path>.verdict.bak` before the
first changing write. Unknown keys and other servers are preserved. A file that
does not parse refuses typed instead of being rewritten; for TOML only a plain
`[mcp_servers.verdict]` table is merged — inline/dotted spellings refuse
rather than guess, and a header, key, or comment counts only outside strings
and comments. A file that ends inside an unterminated TOML string, or whose
`[mcp_servers.verdict]` line opens one, refuses too: the merge never writes
keys the next run would read as string content. The file's own line endings
are kept. Fix or remove the entry by hand and re-run.

`opencode` (`"mcp"` map with `{type:"local", command:[...]}`),
`copilot` (`"servers"` map), and `goose` (`extensions` YAML) are print-only:
`--write` refuses typed naming the host, so copy the printed snippet into the
host config by hand. The `gemini`, `qwen`, and `windsurf` shapes are confirmed
from the host's docs; the `opencode`, `copilot`, and `goose` shapes are
unverified live (offline), so check the printed snippet against the host's
docs before trusting it.

The server is experimental until 1.0: tool shapes may still move, while the
envelope keeps its `schema_version` contract.

### Pre-push probe (local gate before CI burns)

Drop the combined gate into `.git/hooks/pre-push` (executable) so a stale HEAD
or an open critical fails locally instead of burning CI:

```bash
#!/bin/sh
verdict gate owner/repo 123 --max-severity critical --require-head --live auto || exit 1
# exit 0 only when no open critical+ findings stand AND the fetched HEAD
# is the posted HEAD; exit 1 names which dimension failed on stdout.
```

Recovery lives where fetch behavior lives: the `stale-head`/`missing-post`
check line names the re-fetch command — the `--base-sha <short-posted-head>`
advance form when `post.json` carries a head, the plain `fetch` when there is
nothing to advance from (see the `report --completeness` paragraph above and
the Finalizer loop under Findings store + triage authority). Local bundles
keep the `fetch-local` form. The fail-open default is deliberate: the local
dimensions (tier hits, HEAD-reviewed) are ground truth the operator already
paid to collect, while a failed lookup proves nothing about live threads —
conflating "network dead" with "threads open" would turn every outage into
a red gate and teach operators to bypass it. `strict` exists for the
opposite bet: a merge gate that must refuse rather than pass blind, at the
cost of going red whenever the network does.

Offline-safe by construction: every gate run under the default `--live auto`
posture ends with the live-thread recheck
that warns (`live thread recheck unreachable`) and proceeds on the local
dimensions when the network is unreachable — the hook stays fast and never
exits 3 over the network. Operators who need the closed posture opt in
with `--live strict` (or accept the loudly-skipped local verdict with
`--live skip`).

### Doctor preflight

`doctor` preflights one code host with no repo/PR args and no writes: the
host CLI, its auth, and `storage-writable` hard-fail the run (exit 1);
`token-scopes`, `ps`, `git`, and `api-reachable` warn only and never fail it.
`--provider github|gitlab|gitcafe|forgejo|bitbucket|azure` takes the same enum
and the same `bad-provider` refusal as `fetch --provider`, and defaults to
`github`, so a bare `doctor` keeps its GitHub rows byte for byte. Each host
reads through its own CLI — `gh`, `glab`, `cafe` — and `forgejo`, `bitbucket`,
and `azure` check the tool their fetch path shells (`curl`, `curl`, `az`) plus
the credential variables that path reads (`FORGEJO_HOST`+`FORGEJO_TOKEN`;
`BITBUCKET_TOKEN` or `BITBUCKET_USERNAME`+`BITBUCKET_PASSWORD`;
`AZURE_DEVOPS_ORG`+`AZURE_DEVOPS_EXT_PAT`), reporting presence only and never a
value. A host's rows carry its own names (`gh-cli`/`gh-auth` for GitHub,
`glab-cli`/`glab-auth` for GitLab), so a preflight never shows a `gh` row for a
host that does not use `gh`.
The scope rows reuse the single existing `gh api user` scopes-header read —
no new network calls. Grants below the baseline (`repo`/`public_repo` for the
reads the tool performs) warn on the `token-scopes` row; grants above it warn
on a separate `token-scopes-excess` row naming the excess scopes (baseline is
the repo family plus `read:*` — anything beyond, e.g. `delete_repo`,
`admin:*`, `workflow`, `gist`) with a least-privilege re-mint hint. Minimal
grants emit no excess row, fine-grained tokens keep today's advisory row, and
the run still exits 0 — excess scope never fails a preflight. `--json`
carries every row in `data.checks`.

Sigs follow the Sig contract under Findings store.

### Guarded submission and operator surfaces

`post --review-file <path>` submits a review another tool wrote. The default
is `<bundle>/review.json`, and the flag changes only where the review is read
from: `post` still runs the same guarded path — review-input checks
(including `schema_version`), anchor validation against the pinned hunks,
suggestion rendering, stale-head and duplicate-payload guards, the typed
policy gates, and the severity floors. A missing path refuses
`missing-review-file`; `--dry-run` prints the validation and payload summary,
writes `findings.dryrun.json`, and posts nothing.

Approval intent comes from the review body, not from `--approve=false`: a
review file with `approved: true` can approve even though `--approve=false` is
the default. A binding verdict (`APPROVE`/`REQUEST_CHANGES`) still needs
`--bind`; without it, `post` downgrades to `COMMENT`. A binding verdict on your
own PR is also downgraded to `COMMENT`, and `--force` does not override that
guard. A bundle whose stored PR state is not `OPEN` refuses `pr-not-open`; an
unknown state warns and proceeds. `--dry-run` still performs the live head read that backs the stale-head guard,
and it still performs the live-thread read a selected `no-live-threads` policy
requires; only the stored-open recheck is skipped. It is not fully offline. A failed
live-head lookup warns. A failed live-thread lookup under `no-live-threads`
refuses a binding approval with `requirement-unmet`; a non-approving run
continues without a thread-gate warning.

A review whose text carries a secret-shaped value refuses
`post-secret-shaped` before any host call: the scan reads the summary body and
each finding's body and suggestion, and it names the site (`finding 2`) and the
matched shape (`aws-access-key-id`, `github-token`, …), never the value. A
`--dry-run` warns in the same words and exits 0 instead, so a preview never
refuses what it cannot post. `--allow-secret-values` publishes the review
anyway, and `post.json` records the shapes it let through in
`secret_shapes_allowed[]` — the receipt proves the override and what it cost.

`triage --dry-run` computes one status flip and writes nothing: it resolves
the sig, applies the rationale rule, and prints `would set <sig> -> <status>`
(`data.dry_run: true` in `--json`, and a `next` that says nothing was written).
`triage --force` waives the 30-char rationale and stamps `forced without
rationale` on the record. Neither flag reaches the network.

`check-tree --expect <prefix,...>` is the clean-tree gate for mutating modes. A
dirty path is accepted only when it starts with one of the comma-separated
prefixes (a rename is judged by its destination path); any other entry prints
under `check-tree: unexpected dirt:` and exits 1. Without `--expect` any dirt
fails. The verb's own `.verdict/state/` scratch is always ignored, so a bundle
never trips it.

`upgrade --version <v>` pins an exact release (`X.Y.Z` or `vX.Y.Z`), which is
also the rollback route: re-run it with the version that worked. An
installer-managed binary downloads, verifies, smoke-tests, and atomically
replaces itself with that release; global npm/pnpm/bun/yarn installs delegate
the pinned version to the owning manager; Homebrew refuses `--version` because
the formula owns version selection. `--dry-run` prints the channel and the
planned action, and a malformed tag refuses `upgrade-bad-version`.

Shell completion ships in the npm package as `scripts/verdict-complete.sh`.
Sourcing it registers bash (`complete`) and zsh (`compdef`) completion for the
verbs, each verb's flags, and the enum values (`--provider`, `--status`,
`--rigor`, `--preset`, `--live`, `--max-severity`, `--approve`). It hides
`mcp`: the stdio transport is started by a host config, not typed.

Fetch resilience is the shared bounded retry (fetch, post, and the provider
CLIs): up to three attempts, only for transient failures. A secondary rate
limit honors `Retry-After` (or the reset marker) up to a 60 s wait; the host's
internal-error note sleeps 1 s then 2 s; auth, validation, and missing-binary
failures never retry, and a canceled wait aborts instead of sleeping past the
signal. A failed call does not prove the host did not write: a lost POST response
can make the retry publish the same review twice. The duplicate guard runs before
submission, not between attempts, so re-check the PR after a retryable post failure.

Paging is layered on the same file list. When the hosting CLI returns exactly
its 100-file cap, `fetch` re-reads the list with pagination to the provider
ceiling (3000 files), rewrites `PR.json`, and records the provenance in
`filesource.json`; a paging failure warns and proceeds with the CLI's first
100, marked `source: "cli"` with the error. The host's own diff rendering
limit is separate: when GitHub refuses to render a diff (its 300-file limit)
and both diff reads fail, `fetch` refuses the typed `upstream-diff-too-large`
with the range/paths narrowing hint, never the local byte-cap `diff-too-large`
or a generic `fetch-failed`.

`sensitive.txt` is the advisory manifest, not a gate: `fetch` lists
secret-adjacent paths with the matched reason, `fetch --json` publishes its
path under `data.artifacts.sensitive.txt` (null when absent), and the prompt
dump fences it when present. Nothing withholds or blocks on it, and its header
asks reviewers to name the path, never quote a flagged value.

## Your agent, launched (`run --agent`)

`fetch` ends with a bundle and a prompt nobody ran. `run` closes that gap
without becoming an orchestrator: it launches **your** agent against an
existing bundle, one process per round and one shot by default, and hands the
result back to the verbs that already exist.

```bash
verdict fetch-local acme/app work              # writes .verdict/state/local-work/
verdict run acme/app local-work --agent claude # launch your agent; it writes review.json
verdict record acme/app local-work             # the existing validation path, unchanged
```

For a PR bundle the middle step is `verdict run acme/app 123 --agent claude`
and the handoff is `verdict post acme/app 123`. `run` never records, posts,
merges, or pushes; with `--rounds N` it repeats only the launch→check pair,
up to N rounds, and still records nothing: `next` in its envelope names the
command it did not run.

`run` invokes your agent only when you ask: one process per round, one bundle. No
daemon, no queue, no scheduler, no webhook, no model selection, and by default
it never reads prose: with `--rounds N` it runs the published `CheckReview` shape
checker over review.json (paths and codes only) to build the next round's fix
list.

### What `run` asserts

The contract is the artifact, not the output. After the process exits, `run`
requires exactly one fresh `.verdict/state/<bundle>/review.json` — present, a
regular file, and not older than the launch record it wrote first — then
stops. `record`/`post` validate that file unchanged, so a review `run` accepts
is a review they accept.

The freshness stamp has one-second granularity, so the guarantee is "not older
than the launch", not "written by this agent". A `review.json` left by an
earlier run in the same second the launch record was stamped reads as fresh by
design: refusing it would fail an agent whose write landed inside that second.
The same window covers a silent later round on a whole-second filesystem: inside
the previous review's second it reads as fresh and the loop stops by the
no-progress rule, not as stale.
`run` records whether the artifact was fresh and how large it was, and a refusal
names both timestamps, so the limit is visible after the fact rather than hidden.

The directive handed to the agent is short and tool-generated: the subject,
the bundle path, the single review file to write, and the boundary. `run` does
not re-render the diff — `fetch --show-prompts` is that (fetch-time, with your
privacy exclusions applied); the agent reads the bundle with its own tools.

With `--rounds N` (2–5) an invalid `review.json` does not end the run: the
next round re-sends the directive with the prior review bytes (capped at 16 KiB,
and cut shorter when Windows argv escaping would carry the whole command line
past 32,767 characters; full bytes in `.run.rounds/NN.review.json`) and the
checker's fix list fenced under a fresh per-round nonce, and the loop stops on the
first valid round. Each round rewrites the launch record just before it spawns,
so the freshness assert is per round. Only shape errors re-prompt (warnings never
do); a crash, a timeout, a missing or stale artifact, or a byte-identical repeat
stops the loop at once. `--json` carries the loop in `data.repair` (rounds,
rounds to valid, per-round hashes, problems); each round's bytes stay in the
bundle's `.run.rounds/` files.

### Agents, arguments, and `--print`

Agent definitions are data: a name, a binary, and the non-interactive argument
shape (`claude -p`, `codex exec`, `cursor-agent -p`, `opencode run`). Adding an
agent is a new table row, not a new code path; the launcher branches on none of
it. The argv it builds is
`<binary> <agent flags> [<your arguments>...] <directive>`.

| Piece              | Meaning                                                                                                                                                         |
| ------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `--agent <name>`   | Required. An unknown name refuses `unknown-agent` and lists the known ones.                                                                                     |
| `--timeout <secs>` | Watchdog budget, default `300` (the same default `verify` uses). `--timeout` is per round.                                                                      |
| `--rounds <n>`     | Total launch→check rounds, default `1` (today's one-shot), range 1–5. `-- <args>` passes through every round.                                                   |
| `-- <args>`        | Everything after the bare `--` goes to the agent unchanged: its own model, cost, and turn flags are yours.                                                      |
| `--print`          | Renders the exact command (POSIX-quoted) and exits `0` without resolving, spawning, or writing. The escape hatch for harnesses that build their own invocation. |

### Refusals, exit codes, and provenance

Every refusal code the binary can emit is listed with its cause and its fix in
[`docs/troubleshooting.md`](troubleshooting.md).

Every way `run` can fail is typed, carries a `try:` hint, and leaves the
agent's own failure visible to CI:

| Code                    | When                                                                            | Exit        |
| ----------------------- | ------------------------------------------------------------------------------- | ----------- |
| `agent-missing`         | the binary is not on `PATH`                                                     | 1           |
| `agent-spawn-failed`    | the binary resolved but could not be started                                    | 1           |
| `agent-timeout`         | the watchdog killed it after `--timeout` seconds                                | 1           |
| `agent-failed`          | it exited non-zero — **its exit code is the process exit code**                 | the agent's |
| `no-review-artifact`    | it exited `0` and wrote no `review.json` (or a non-file)                        | 1           |
| `stale-review-artifact` | `review.json` is older than this launch (an earlier run's)                      | 1           |
| `unknown-agent`         | the name is not in the agent table                                              | 1           |
| `agent-record-failed`   | the launcher could not write its provenance record                              | 1           |
| `repair-exhausted`      | every round left an invalid `review.json` (or a round repeated the prior bytes) | 1           |
| `rounds-path-unsafe`    | the bundle's `.run.rounds` is a symlink or another non-directory                | 1           |

An agent exiting `1`, `2`, or `3` is indistinguishable on the exit code alone
from verdict's own refusal, usage, and canceled codes; `--json` disambiguates
them through `error.code`, and the envelope's `data.exit` records what the agent
itself returned.

Plus the shared codes: `missing-bundle` (fetch first), `bad-flags` (no
`--agent`, a `--rounds` outside 1–5, or `--print` with `--rounds` greater than 1),
`bad-timeout`, `canceled` (exit 3), and usage errors (exit 2).
With the default one shot there is no retry, no re-prompt, and no repair
pass: fix the invocation and run again. With `--rounds N` the only repetition
is the launch→check pair: an invalid `review.json` re-prompts the agent with
the checker's fix list, up to N rounds, and exhaustion refuses
`repair-exhausted`.

The launch is recorded in `.verdict/state/<bundle>/launcher.json`: agent name,
resolved binary path, the argv shape (directive replaced by `<prompt>`,
secret-shaped operands and secret-named environment values redacted), start
and end timestamps, duration, exit code, timeout flag, artifact freshness and
size, and a redacted output tail. Each round's full combined output stays in
`.verdict/state/<bundle>/.run.rounds/NN.out` (crash rounds too) and the last
round's output also stays in `.verdict/state/<bundle>/.run.out`. Both are evidence, not gates. With
`--rounds N` the record also carries `repair` (rounds, rounds to valid, final
valid) and top-level `attempts[]` (per-round prompt and review hashes, check verdict, problems),
and each round's bytes land in `.verdict/state/<bundle>/.run.rounds/`.

### Network

Verdict itself makes no network calls in `run` — spawning is local. Your agent
is a program with your credentials: it may call whatever provider you
configured, and the diff goes wherever your agent sends it. That is your
agent's contract, not verdict's; run in CI with a key scoped the way you scope
any other model call.

## Model wiring

This kit uses whatever agent you invoke — your current model is the review model.
Lenses can run cheaper: map each lens to a smaller sibling of the main model
(e.g. flagship → sonnet-class) via the `lens:<label>` dispatch template, at most
~12 files and 10 findings per lens. Formation is roles, never slugs: strongest reasoner
synthesizes, 2+ diverse cheaper models lens, different families when possible.

For arena-style parallel runs, pin the panel outside the skill: `REVIEW_PANEL` env
(comma/space separated) or the typed `panel` default in `.verdict/config.json`
(and per-scope overrides). `fetch` resolves it to `.verdict/state/N/panel` — first
entry synthesizes, the rest staff lenses, used exactly in order, never
substituted. Same panel every run keeps verdicts comparable and costs capped;
no panel means the operator picks per run. Slugs live in your config because
they rot; the skill stays name-free.

### Cost control (budget, estimate, consent)

Cost accounting (`report --cost`, per-run `cost.json` with effort tiers) grows teeth
only when a spend guard is set: `REVIEW_BUDGET_USD` env (wins) or the typed
`budget_usd` default in `.verdict/config.json`, resolved per file group. Unset
leaves a group with no ordinary monetary guard; the personal `spend.budget_usd`
authorization limit still applies. No guard, no ceiling, and no explicit effort
plane means no estimate line and no receipts.

Each resolved file group has its own projection from comparable past runs
(every `.verdict/state/*/cost.json` record, same tier rules as the rollup; ties
break toward the deeper tier) and its own `budget_usd` guard. `fetch`/`fetch-local`
print a pre-run `» estimate` line before any network. A group over its guard, or
the exact sum of group projections over the personal limit, refuses with typed
`budget-exceeded` unless the run carries `--consent-over-budget`; consented runs
append one receipt to the bundle's `budget.json`, which `report --cost` surfaces
as a `receipts:` line (and a `receipts` key in the JSON cost object). Repository,
environment, and flag defaults can never raise the personal limit, and no
repository key grants consent. Any unknown group projection makes the whole-run
total unknown: with `require_known_estimate` the run refuses typed
`budget-unknown` until fresh per-invocation consent, whose receipt records
`unknown-estimate` and invents no USD figure; without it unknown stays
unenforced. `enforce_cap: true` refuses typed `unsupported-cap` before any
budget check, force, or consent — the guard is a pre-run projection, never a
billing cap. Estimates cover the review run about to start, not the fetch reads.

Lens evidence and group costs are read by two consumers with different
strictness, and both are visible in `report --cost` and the typed refusals.
Aggregators — the rollup, the group estimate, the receipt — price the fields
they know and ignore top-level fields a newer writer added, so one record's
stray key never voids the history an estimate is built from. Certification of
lens evidence is version-scoped: the newest record bound to the capture by
`run` and `inputs_sha256` may carry only the top-level fields its
`schema_version` defines — an extra field refuses by name and version (for
example, one carrying a stray `notes` key is told that v2 does not define it) —
and a record bound to the run at a version this build does not certify refuses
as `version-unsupported` rather than being re-read as v2. A `schema_version`
this build cannot read at all — `null`, a boolean, an array, an object, an
empty string — is `malformed`, and it refuses the same way: presence is the
key, so an unreadable declaration is not an absent one and never lets an older
record stand in for it. All three leave the capped group `usage-unknown`, never
zero. A record that declares no `schema_version` key at all is the one case that
is skipped rather than refused: it makes no claim about its writer's version, so
the newest _declared_ record decides, even when an unversioned record matched
later. Nothing loosens the fail-closed checks: a
foreign capture, a duplicated key, an unreadable entry, and a mismatched
binding all still refuse.

When the agent's provider reports prompt caching, per-model `cost.json` entries
may carry `cache_read_input_tokens` (served from cache) and
`cache_creation_input_tokens` (spent writing the cache) — both subsets of the
sibling `input_tokens`, never additions; absent or `null` reads as unknown, never
zero. `report --cost` rolls the split into a top-level `cached_input_tokens`
scalar (shown as `cached=N` on the human line) while totals stay
`input + output` exactly; the script never prices cached tokens (`est_usd`
remains the agent's own math, as today).

#### Measured usage (explicit usage handoff)

When the operator hands over usage records, the budget gate prices
comparable runs from them instead of the self-reported `est_usd` — read
locally, never over the network, and never discovered on its own. Point
`VERDICT_USAGE` at one file (a single record, an array of records, or a
`.jsonl` log with one record per line) or a directory of such files, all in
the contract shape published as `schemas/usage.schema.json`:

```json
{
  "schema_version": "1",
  "source": "claude-code",
  "model": "claude-sonnet-4-5",
  "tokens": { "input": 3000, "output": 170, "cache_read": 800 },
  "usd": 2.5,
  "started_at": "2026-09-01T10:00:00Z",
  "ended_at": "2026-09-01T10:02:00Z"
}
```

Only `schema_version`, `source` (a free-form measurer name), and the
`started_at`/`ended_at` window are required; `model`, `tokens`, and `usd`
ride along when the measurer knows them. If the agent running the review
knows its real usage, it writes one such record (see `SKILL.md`) and reports
the path; harness exports (Claude Code transcripts, opencode exports) are
converted with the optional scripts in `examples/usage-adapters/` — the
binary never opens those files itself.

A record is joined to a `.verdict/state/*/cost.json` entry only when its window
brackets that record's mtime, so an unrelated session never prices a review.
The handed USD replaces the self-reported `est_usd` for that run; records
without a handed twin keep their self-report. Records without a priced
total are never converted to USD (the tool never prices tokens).

The estimate names its basis whenever a handed record contributed:
`data.estimate.basis` is `measured` (every priced sample in the projected
tier came from handed usage) or `mixed`, with `harness[]` naming the
record `source` strings and `measured_runs` counting them; the human
estimate line and an over-budget refusal carry
`[measured from N harness session(s)]`. Those keys are absent — and the output
stays byte-identical — when nothing was handed over, so a
self-reported estimate is never silently labeled as measured. A named path
that is missing, oversized, or malformed refuses typed (`bad-usage`) before
the bundle is written: an explicit handoff never silently degrades.

## Onboarding (scaffolder + prefs file + skill installer)

New operators onboard in one offline command — no network, ever:

```bash
verdict init --preset standard --yes   # chill|standard|strict, default standard
```

`init` writes the standard local control files, then runs check-tree
validation (same filter: own `.verdict/state/` scratch ignored). Existing files
print a preview and need `--yes`/`--force` (or, in human mode, an
interactive confirm when stdin is a tty) before overwrite — under `--json`
there is no prompt: the run refuses typed `needs-confirm` instead. The same
preview-then-confirm idiom the installer uses. Re-running with the same preset reads as `keep` (idempotent).

Style presets map to panel strictness plus KB retirement thresholds (the KB
starter is identical across presets):

| preset     | `defaults.budget_usd` | `defaults.panel` members   | `defaults.kb_policy`                                  |
| ---------- | --------------------- | -------------------------- | ----------------------------------------------------- |
| `chill`    | `2.00` (lenient)      | none (comments only)       | `min_judged: 10`, `max_fp_rate: 0.7` (retires rarely) |
| `standard` | `0.50` (default)      | `main` (one synthesizer)   | `min_judged: 5`, `max_fp_rate: 0.5` (code defaults)   |
| `strict`   | `0.10` (tight)        | `lead`, `lens-1`, `lens-2` | `min_judged: 3`, `max_fp_rate: 0.3` (retires quickly) |

`.verdict/kb.jsonl` starts with two starter rules (`check-error-paths`,
`verify-before-claim`, both `status: active`, counters zeroed) ready for
`learn --status`. The `verify-before-claim` starter uses scope `*`, so it
recalls on every bundle until you narrow it — tune scopes early, then replace
the panel placeholders with real slugs — same
panel every run keeps verdicts comparable and costs capped.

The prefs file ends env sprawl without forcing migration. Path:
`${XDG_CONFIG_HOME:-$HOME/.config}/verdict/settings.json` (`$XDG_CONFIG_HOME`
respected, `$schema` key carries a schema hint that is never fetched).
The file carries the four presentation namespaces below plus the typed personal
layer (`review`, `spend`, `privacy`) described under "Typed repository
configuration" above, so it accepts eight top-level keys in total: `$schema`, `output`,
`layout`, `color`, `publish`, `review`, `spend`, `privacy`. Anything else —
server-shaped or `org.*` keys included — refuses typed `bad-settings`. The
presentation sample:

```json
{
  "output": { "format": "text" },
  "layout": { "compact": false },
  "color": { "mode": "auto" },
  "publish": {
    "footer": true,
    "attribution": "product",
    "copyright": "",
    "logo": true,
    "logo_url": "https://raw.githubusercontent.com/stainless-code/verdict/main/apps/docs/public/logo.svg",
    "diagrams": {
      "structural": true,
      "behavioral": false,
      "expanded": false
    }
  }
}
```

- `output.format` (`text`|`json`) is the `--json` default.
- `layout.compact` (bool) suppresses advisory progress lines (the `panel:
none pinned` and `learnings` notes); pinned-panel and warning lines stay.
- `color.mode` (`auto`|`always`|`never`) controls ANSI on human progress
  (warnings/errors); the tool never colours on its own, so `auto`/`never` read
  plain and only `always` colours warn/error. Machine envelopes never carry codes.

Precedence is flags > file > env (`REVIEW_OUTPUT_FORMAT`,
`REVIEW_LAYOUT_COMPACT`, `REVIEW_COLOR_MODE`, plus `REVIEW_PUBLISH_FOOTER`,
`REVIEW_PUBLISH_ATTRIBUTION` (`off`|`product`|`full`), `REVIEW_PUBLISH_ATTRIBUTION_TEXT`,
`REVIEW_PUBLISH_COPYRIGHT`, `REVIEW_PUBLISH_LOGO`, `REVIEW_PUBLISH_LOGO_URL`
for the footer below) > defaults; global flags
`--color <mode>`, `--compact`/`--no-compact`, `--no-json` work on any verb and
in any position — before the verb, between the verb and the repo, or after the
pr. A bare `--` ends positional parsing: every later token is a positional,
never a flag.
`NO_COLOR` applies only when color is otherwise unset. Any other top-level
key (including server-shaped or `org.*` namespaces) refuses with typed
`bad-settings`; any credential-shaped key at any depth (`token`/`key`/`auth`/
`secret`/`password`/`pat`/`credential`/`bearer` and compounds) refuses with
typed `bad-settings-credentials` — credentials never live in the file.
Absent file (or a file holding only defaults) keeps every gate
byte-identical to today (marker-first, degenerate-body bar unchanged); only
the rendered bytes gain the default footer block below — no forced
migration, no env removed.

## Install routes and where commands run

One execution model across every route. Run the CLI from the repo under
review: `.verdict/state/` is created in the current directory, never beside the
tool. The CLI is the `verdict` binary. Caller tracing, test checks, and
`verify` all run repo-scoped in that directory, so reviewing another repo
means running from its checkout (or accepting that the bundle lands wherever
you stand).

The shipped `verdict` is one static Go binary (`make build` writes
`bin/verdict`) that needs no shell wrapper. `VERDICT_BIN` is not the binary's:
the skill shim and the TypeScript client both consult it. The shim
(`skills/verdict/scripts/verdict`) resolves it first, then `<skill>/bin/verdict`,
then `verdict` on `PATH`; `resolveBin` in `clients/ts` takes it ahead of the
platform package. A plain `verdict` call consults neither variable.

- **Binary** (`install.sh` / `install.ps1`, the default): the script fetches
  one release archive, `checksums.txt`, `commit.txt`, and
  `checksums.txt.sigstore.json`, verifies the cosign identity/issuer, the
  trigger pin (`--certificate-github-workflow-trigger`), and the build-commit
  pin (`--certificate-github-workflow-sha`) over the checksums and the archive
  SHA-256, extracts only `verdict`, and atomically renames a staged file over
  the target. `$VERDICT_INSTALL/verdict` is the binary
  (default `~/.verdict/bin/verdict`); `$VERDICT_INSTALL/.verdict-installer` is
  the installer receipt. No root is needed or requested. Any verification
  failure leaves the old binary in place; the download is never executed
  before verification. A release that publishes no `commit.txt` is refused:
  an install is never left unpinned. When cosign is present the sigstore
  bundle is mandatory.
  Without cosign the installer warns loudly and verifies the checksum only;
  `VERDICT_REQUIRE_SIGNATURE=1` (Windows: `-RequireSignature`) fails closed.
  `VERDICT_VERSION` pins a release (`vX.Y.Z` accepted); the default `latest`
  resolves through the Releases redirect, never the rate-limited API (Windows
  `-Version`). `VERDICT_DOWNLOAD_URL` overrides the release base for mirrors.
  PATH is touched only by appending one marked line (`# verdict installer`)
  when the install dir is missing; `VERDICT_NO_MODIFY_PATH=1` or
  `--no-modify-path` (Windows `-NoModifyPath`) opts out, and `install.ps1`
  updates the user PATH only. Offline installs pass a local archive
  (`install.sh ./verdict_1.2.3_linux_amd64.tar.gz`) with its sibling
  `checksums.txt`, `commit.txt`, and optional bundle, or point
  `VERDICT_DOWNLOAD_URL` at a `file://` mirror; local-file installs make no
  network call.
  `install.sh --uninstall [--remove-path]` and Windows
  `install.ps1 -Uninstall [-RemovePath]` remove only installer-managed files
  and their own PATH line;
  a `verdict` without the receipt is refused as foreign. The scripts are
  release assets, not npm tarball contents.
- **Updating** (`verdict upgrade`): explicit only — no background check, no
  nag, no daemon, and the review path never calls it. The command resolves
  `os.Executable()` through symlinks and maps the result to one channel:
  installer-managed (a receipt beside the running binary that no package
  manager or skill copy owns, in `~/.verdict/bin`, or in `VERDICT_INSTALL`)
  does a verified self-replace (archive + `checksums.txt` + `commit.txt` +
  sigstore bundle; cosign identity/issuer, trigger, and build-commit pins when
  cosign is present, loud checksum-only fallback, and
  `VERDICT_REQUIRE_SIGNATURE=1` fails closed; SHA-256; extract
  only the binary; `--version` smoke test; atomic replace with rollback).
  Global npm/pnpm/bun/yarn installs run that manager's global update
  (`npm i -g @stainless-code/verdict@latest`, `pnpm add -g …`,
  `bun add -g …`, `yarn global add …`) after printing the exact command and
  with no prompt; Homebrew runs
  `brew upgrade stainless-code/tap/verdict` (the formula owns the version, so
  `--version` refuses). Project-local `node_modules` (the manifest/lockfile
  owns it), skill copies (`npx skills update` / the skill bootstrap),
  sudo/admin, unwritable targets, and unidentifiable paths refuse typed with
  the exact remediation command — never a silent privilege escalation and no
  download in a refusal path. `--dry-run` prints the channel and planned
  action and touches nothing; `--json` emits the standard envelope;
  `VERDICT_INSTALL_METHOD` (`installer-managed`, `npm`, `pnpm`, `bun`,
  `yarn`, `homebrew`, `project-local`, `skill`, `unknown`) overrides
  detection for tests. On Windows the
  running `.exe` is never overwritten: the verified new binary is copied to
  `%TEMP%`, spawned as a helper that waits for the parent PID, renames the old
  binary to `.old`, moves the new one in, smoke-tests, writes the receipt,
  deletes `.old`, and restores the old binary on failure.
- **Uninstalling** (`verdict uninstall`): installer-managed installs only.
  Removes `verdict`, the `.verdict-installer` receipt, and (with
  `--remove-path`) the marked `# verdict installer` shell-profile line
  (`install.ps1`'s user PATH entry on Windows); other files are untouched.
  npm/pnpm/bun/yarn/Homebrew/project-local/skill copies refuse typed with the
  owning removal command (`npm uninstall -g …`, `brew uninstall …`,
  `rm -rf <skill dir>`, …). `--dry-run` and `--json` are supported.
- **Registry** (`npx skills add stainless-code/verdict`): the primary path is
  the GLOBAL binary (curl/npm/brew above), which the skill copy resolves on
  PATH through `scripts/verdict` (`VERDICT_BIN`, then `<skill>/bin/verdict`,
  then `verdict`). `npx skills` is scoped to the skills dir and never
  installs the binary; `install.sh` is not shipped in the npm tarball and
  never runs here. With no global binary, run the one-time verified
  `sh <skillsdir>/verdict/scripts/verdict bootstrap`: it fetches the pinned
  release's `install.sh` once, that installer verifies the sigstore bundle
  (when cosign is present) and the SHA-256, and the binary lands at
  `<skillsdir>/verdict/bin/verdict`. It is idempotent and offline afterwards;
  no review ever downloads. Refresh the skill with `npx skills update`.
- **Source checkout** (`git clone` + `make build` + `./install-skill.sh`):
  symlinks `skills/verdict` into the agent skill dirs (nine agents,
  `--detect`/`--all`, doctor-gated). Updates via `git pull`;
  `install-skill.sh` refuses outside a git checkout. `hooks/pre-commit`
  wiring applies to the checkout only.
- **Embedded skill** (`verdict skill`): the binary carries its own `SKILL.md`,
  `prompts/`, `schemas/`, `packs/`, and `scripts/verdict`, so the served skill can
  never skew from the binary. In the repository they are the one
  `skills/verdict/` directory, which holds nothing else. The skills CLI
  (`npx skills add stainless-code/verdict`) copies that directory and no
  more, so the registry install and `verdict skill install` write the same
  files. `verdict skill` prints `SKILL.md` (default
  `--format md`); `--format json` is one standard envelope with
  `name`/`version`/`files`; `verdict skill files` lists every embedded path.
  `verdict skill show <path>` prints one inventory file byte-identical (a
  path from `skill files`; with `--json` one envelope carries `path` and the
  same bytes in `content`), and a path outside the inventory — traversal,
  absolute, or unknown — refuses typed (`skill-path-not-inventory`) and names
  `skill files`.
  `verdict skill status [--dir <skills-dir>|--agent <name>...|--detect] [--diff] [--json]`
  is the read-only drift check: it writes nothing and
  reports each target's create/update/keep/blocked result plus the receipt
  versus embedded version, reusing the install plan. It exits 0 when every
  target is in sync and 1 when drifted (a typed refusal still exits 1), so CI
  can gate on it without `install --dry-run`. `--diff` adds one unified diff
  per changed file, capped at 64 KiB overall with an explicit truncation
  marker and `truncated: true` in the JSON entry.
  `verdict skill install` (flags: `--dir <skills-dir>`, `--agent <name>`,
  `--detect`, `--force`, `--dry-run`, `--json`) writes the payload plus a
  `.verdict-managed` receipt (file-atomic, receipt last), previews
  create/update/keep/blocked (`--dry-run --json` stays a drift preview: `update`
  means the installed copy is stale, `keep` means current, `blocked` needs a
  human), refuses
  a target without a verdict receipt unless `--force`, never removes foreign
  files, never prompts, and makes no network call. A target that is itself a
  symlink refuses (`skill-target-file`), and a payload directory that escapes
  the target through a symlink refuses (`skill-foreign-conflict`) before
  anything is written. `--agent` accepts
  `agents`, `claude`, `codex`, `cursor`, `opencode`, `copilot`, `gemini`,
  `goose`, `qwen`, and `windsurf`; `--agent agents` (and no flag) target the
  shared `~/.agents/skills/verdict` slot. `--agent` repeats on `install`,
  `status`, and `uninstall`: targets are deduplicated by resolved path and
  handled in argv order, so two agent names that reach one skills dir (for
  example `claude` under `CLAUDE_CONFIG_DIR=~/.agents` and `agents`) plan and
  act once, under the first name. `--dir` stays a single target. `--detect` installs into every one
  of those agents whose skills dir already exists and holds a file, and names
  each target's result; with none present it refuses typed
  (`skill-no-detected-agent`) and asks for `--agent <name>`. It cannot be
  combined with `--dir` or `--agent`, every target keeps the receipt and
  `--force` rules above, and a blocked or conflicting target refuses before any
  target is written. It does not create a missing agent dir, and unlike
  `install-skill.sh --detect` it skips `~/.agents/skills` when that dir is
  absent or empty.
  `verdict skill uninstall [--dir <skills-dir>|--agent <name>...] [--force] [--dry-run] [--json]`
  deletes only the inventory paths the receipt owns and
  then the receipt, pruning the payload directories it empties; every foreign
  file survives. A target that is a symlink refuses (`skill-target-file`), and
  an inventory path that escapes the target through a symlinked directory
  refuses (`skill-foreign-conflict`) before any file inside it is removed. A
  target without a receipt refuses typed
  (`skill-dir-foreign`) unless `--force`, which still deletes only inventory
  paths; a target that was never installed refuses (`skill-not-installed`),
  and `--dry-run` lists the plan and deletes nothing. `verdict uninstall`'s
  skill-copy refusal names this verb only when the copy's root is the
  `verdict` skill dir; any other skills dir is named in place, so the hint
  can never remove a sibling.

## Publish footer + attribution knobs

Every posted body and every `report --output` file ends with one merged
block at the end:

```markdown
---

<!-- verdict-footer -->

> <a href="https://stainless-code.com/verdict"><img src="https://raw.githubusercontent.com/stainless-code/verdict/main/apps/docs/public/logo.svg" alt="Verdict" width="18" height="18"></a> Reviewed with [**Verdict 0.6.0**](https://stainless-code.com/verdict) · standard tier · cost $0.05 · 4 files · [review `5722667325`](https://github.com/acme/app/pull/9#pullrequestreview-5722667325)
```

Fields degrade factually, never blank, never failing the run: an unreadable
version reads `0.0.0-unknown` (the same string `--version` prints), unpriced
spend reads `cost unrecorded`, uncountable
files read `files unrecorded`, and the review link renders only when a
completed post record carries it (never invented pre-post). When the post
record's head matches the bundle HEAD the link is omitted instead of pointing
at the stale review (a same-commit repost supersedes the stored id). The attribution
line carries a product-linked logo and a linked bold product name with the
version inline and space-separated — never in brackets (they would collide
with the link text) or parens.

The `<!-- verdict-footer -->` comment is a machine divider between content
and footer. Re-rendering a body that already carries a rendered block strips
the old block at the divider and appends a fresh one, so the result is
byte-identical (no stacking). Never hand-write the marker — bodies carrying
it get their rendered block replaced.

Knobs live under one `publish` namespace in the settings file (see Onboarding
for the path and shape): `publish.footer` (bool, default `true`) kills the
whole block; `publish.attribution` (enum, default `"product"`) levels the
line — `"off"` keeps the receipt without the sentence or logo, `"product"`
renders the linked name with no maker, `"full"` appends
`by [Stainless Code](https://stainless-code.com)` after the sentence;
`publish.attribution_text` (string, default
`Reviewed with [**Verdict {version}**](https://stainless-code.com/verdict)`,
`{version}`/`{tier}`/`{product_url}`/`{cost}`/`{model}`/`{files_reviewed}`/`{head_sha_short}`/`{attribution}`
substituted — the shared v1 inventory, documented under Section overrides)
rewords the sentence — any other `{placeholder}` refuses typed `bad-settings`;
`publish.logo` (bool, default `true`) drops the image;
`publish.logo_url` (string, default the tracked `apps/docs/public/logo.svg`
raw asset) repoints it — empty reads text-only, never a broken image;
`publish.copyright` (string, default empty = off) appends the operator
notice as its own line. Receipt fields render in `footer-order.md`
sequence (subset, never new fields) — there is no knob beyond that ordering.
Flags `--footer/--no-footer`, `--attribution [off|product|full]`/`--no-attribution`,
`--attribution-text <text>`, `--copyright <text>`, `--logo/--no-logo`,
`--logo-url <url>` win over the file; the file wins over
`REVIEW_PUBLISH_FOOTER`, `REVIEW_PUBLISH_ATTRIBUTION`,
`REVIEW_PUBLISH_ATTRIBUTION_TEXT`, `REVIEW_PUBLISH_COPYRIGHT`,
`REVIEW_PUBLISH_LOGO`, `REVIEW_PUBLISH_LOGO_URL` env; env wins
over defaults. Unknown `publish.*` keys refuse typed `bad-settings`, and any
credential-shaped key at any depth refuses `bad-settings-credentials`.

Confidence is the agent's "would I merge on this review" judgment, informed by evidence depth, coverage, and rule support.
The scale stays 1-5 across
runs so past verdicts stay comparable (no rescaling; a computed score stays
deferred until machine-verifiable inputs exist, with kb-measure X2/X3
outputs as the named trigger). `report --output` renders the store-wide Basis
line; the agent's posted line covers its own review — on a re-review with carried-over
findings the two legitimately differ, and validation compares the posted line
against the review's own comments.

## Mentions in the published text

A review body is written by an agent and published verbatim, so an `@handle`
in its prose would notify that person the moment the review is submitted. The
escape happens once, in the merge that constructs the body into the recorded
bytes — the same call `post` makes for its payload — so the recorded body, the
`post --dry-run` output, and the payload digest carry the same bytes by
construction. Never at submit time: escaping there would make the posted bytes
diverge from the record, the preview, and the digest.

The escape is U+200B ZERO WIDTH SPACE between the `@` and the handle. It is
invisible in rendered Markdown, keeps the body plain text, and no host resolves
a mention across it; a code span would change the rendering, so it is not the
form. The rewrite is idempotent (`@` followed by the space no longer starts a
handle), adds bytes and never removes them, and is not opt-in — there is no
flag or verb for it.

Non-interference is part of the contract. Fenced code blocks (three
backticks or three tildes) and inline code spans are left byte-identical,
because a `@handle` in a sample is not a mention and rewriting it would corrupt
the text. HTML comments — the `<!-- verdict-finding … -->` markers included —
are left untouched, as are the basis line, the diagram block, the coverage
line, and the footer: the escape runs before any of them is appended. A mention
needs the `@` at line start or after a space or tab and the handle to be
`[A-Za-z0-9-]+`, so `user@example.com` is an address and stays as written. Two
residuals stay prose and are not escaped: an indented (four-space) code block
with no fence, and an `@` glued to a blockquote marker (`>@handle`).

Every agent-written string that reaches a reader is covered, not just the review
body: a finding's inline comment `body` is escaped where the entry is built,
before its signature is stamped, so a bundle merged twice keeps one signature.
Re-merging a bundle that predates this change is safe (the rewrite is a fixed
point) but re-keys those entries once, because their stored signatures were
computed over the raw bytes. A `suggestion` is a code replacement and is never
touched — a zero-width space inside a patch would be a silent edit to it. A
comment's machine `brief` is record-only, not published, and stays verbatim.

## Permalink evidence links

Findings link to code instead of pasting it: bodies reference each site as a reference-style `blob/<reviewed-SHA>/<path>#Lx` link (line ranges as `#Lx-Ly`), pinned to the reviewed commit — never live HEAD — so links survive later pushes; code quotes stay at most 3 lines and live only inside collapsed `<details>` blocks, with the visible body link-first. Non-GitHub and local bundles render plain `` `path:line` `` text instead (a blob URL there would 404), and the same degradation applies to `record`/`report` outputs. Link shape is garnish like the basis line: `post`/`record` warn on a wrong-SHA link or a long visible quote and keep the body — reviews never fail over it.

## Change overview (file table + topology)

Large diffs get a script-computed overview in `report --output`: a collapsed
file table (`#`, file, `+/-`, hunks, role) plus a collapsed `flowchart TD`
topology (`PR` node → file nodes, with `uses`/`validates` edges only where an
added diff line names another changed file — unknown relations are omitted,
never invented). Shown collapsed when the bundle holds more than 8 files or
the diff exceeds 24KB (24576 bytes); smaller diffs render byte-identical
output with no overview block. Every table row carries a stable `F1..Fn` id
reserved for future prose annotation — prose would decorate, never replace,
so the table renders identically with or without it. Model one-liners are
explicitly out (a future slice with its own trigger decides those). All cells
and edges derive from the owned bundle stores — file list and deltas from
`PR.json`/diff headers, hunk counts from `hunks.tsv`, roles from `roles.txt`;
no tokens are spent and nothing is hallucinated. Knobs: `publish.overview`
(bool, default `true`) gates the feature and `REVIEW_PUBLISH_OVERVIEW` env
does the same; `--overview` forces it on and `--no-overview` forces it off
(flags win over file over env). With the block on,
`publish.diagrams.structural` (bool, default `true`) gates the topology half
— `false` renders the table alone. Posted agent bodies never carry the block
— `report --output` is its only home, so one truth never forks into two.

## Behavioral diagrams (agent-drawn, opt-in)

Complex logic can ship an agent-drawn mermaid diagram in the posted body —
at most one, collapsed, labeled generated, placed after the metadata
comment. The agent contract lives in `prompts/diagrams.md`: a 7-type
selection map (structure→`flowchart`/`mindmap`, call order→
`sequenceDiagram`, lifecycles→`stateDiagram`, version chains→`gitGraph`,
option matrices→`quadrantChart`, data shapes→`classDiagram`/`erDiagram`,
schedules→`gantt`), a nodes-from-diff-only honesty rule (illustrative content
labeled `illustrative`), copy-exact skeletons pinned to GitHub's rendered
subset (no beta types), a one-diagram cap, and tables-stay-tables. The script
side is grammar-level only: fenced block, keyword in the allowlist,
balanced fences, non-empty. Unknown/beta keywords, unbalanced or empty
fences, and cap extras warn and drop with a one-line `> Diagram omitted: …`
note — a review never fails over a diagram. Mid-body blocks carry their own
marker pair (`<!-- verdict-diagram:start -->` /
`<!-- verdict-diagram:end -->`): the same strip-replace idiom as the footer
divider, bounded because the footer marker cuts to end-of-body and a mid-body
diagram is not terminal. Knobs live under `publish.diagrams`:
`publish.diagrams.behavioral` (bool, default `false` — the block renders
only when opted in), `publish.diagrams.structural` (bool, default `true` —
the topology half above), `publish.diagrams.expanded` (bool, default
`false` — renders the kept block open instead of collapsed). Flags
`--diagram-behavioral` / `--no-diagram-behavioral` (plus
the matching `-structural` / `-expanded` pairs) win over the file; the file
wins over `REVIEW_PUBLISH_DIAGRAMS_BEHAVIORAL` / `_STRUCTURAL` / `_EXPANDED`
env; env wins over defaults. With no diagram in the body the output path is
byte-identical whether the knobs are set or not.

## Section overrides (per-block template fragments)

House style lives in four operator-owned fragments under `.verdict/overrides/`
(bundle-adjacent and repo-local like `.verdict/config.json` and
`.verdict/kb.jsonl` — committable per-repo and check-tree-ignored; not the skill
install tree, not XDG-global settings). One fragment per overridable block:

| Fragment                   | Overridable block                                                                   | Default (absent reads this)                            |
| -------------------------- | ----------------------------------------------------------------------------------- | ------------------------------------------------------ |
| `preamble.md`              | clean-review preamble prose (the `## What we checked` heading in `report --output`) | `## What we checked`                                   |
| `footer-order.md`          | receipt field order, one field per line (subset, never new)                         | `tier`, `cost`, `files`, `link` in that order          |
| `walkthrough-threshold.md` | overview show thresholds (`files: N` / `bytes: N`, `=` works too)                   | `files: 8`, `bytes: 24576`                             |
| `attribution.md`           | attribution sentence (one line; empty omits the sentence)                           | `Reviewed with [**Verdict {version}**]({product_url})` |

Open rules (`footer-order-subset`): receipt order lists a non-empty unique subset of
`tier|cost|files|link` (new fields refuse); attribution stays one line
(`attribution-single-line`);
threshold values stay non-negative integers; `#` comments and blanks are
skipped in the two structured files. The sentence and copyright lines stay
pinned around the receipt so the machine block keeps a stable shape.

Locked invariants (refuse typed `bad-override` naming the invariant): the
verdict marker stays the body first line (`marker-first-line`); body `###`
sections stay agent-owned (`findings-structure`); thresholds stay satisfiable
non-negative integers (`degenerate-guard`); machine metadata comments stay
script-owned (`metadata-presence`); the `verdict-footer`/`verdict-diagram`
dividers stay byte-exact (`divider-shape`). Unknown placeholders refuse typed
`bad-settings`.

Placeholder inventory v1 (fragments and the P1b attribution text share one
validator): `{version}` (tool version), `{tier}` (spend tier),
`{product_url}` (stable product URL), `{files_reviewed}` (`N files` or
`files unrecorded`), `{head_sha_short}` (7-char head, `dirty`, or `unknown`),
`{model}` (first cost-model name, `REVIEW_MODEL`, or `unrecorded`),
`{cost}` (`$n` or `unrecorded`), `{attribution}` (the default sentence, so it
never recurses). Values degrade factually, never blank.

Precedence is flags > fragments > settings file > env > defaults (a
`--attribution-text` flag still beats the fragment; `publish.footer=false`
still kills the block). Validation runs at `check-tree` (authoring-time) and
is revalidated cheaply at `post`/`record`/`report` time with no re-render —
every violation refuses loudly with the broken invariant named (fail-closed;
warn-and-keep never applies to explicit operator intent). With no
`.verdict/overrides/` dir the output path is byte-identical to the built-in
fragments.

## Local review (no PR)

Same review machinery, no GitHub object. `fetch-local owner/repo <name>` builds a
bundle from the worktree instead of the API:

| Invocation                                           | Diff source                                  | Head anchor          |
| ---------------------------------------------------- | -------------------------------------------- | -------------------- |
| `fetch-local owner/repo work`                        | `git diff HEAD` (unstaged + staged)          | `HEAD`, marked dirty |
| `fetch-local owner/repo x --staged`                  | `git diff --cached`                          | `git write-tree`     |
| `fetch-local owner/repo x --range A..B` (or `A...B`) | endpoints (two-dot) / merge-base (three-dot) | `B^{commit}`         |
| `fetch-local owner/repo x --base R`                  | `git diff $(merge-base R HEAD) HEAD`         | `HEAD`               |
| `fetch-local owner/repo x --stash [n]`               | `git diff stash@{n}^ stash@{n}`              | stash commit         |
| `fetch-local owner/repo x -- <paths>`                | any of the above, pathspec-filtered          | as above             |

Flags are mutually exclusive (one diff source per bundle); an empty diff,
including a paths filter that matches nothing, exits 0 with an `empty diff (0 bytes)` warning and the bundle kept: the review proceeds on the empty diff.
Default `git diff HEAD` omits untracked files and warns to stage or commit;
`--staged` includes them after `git add`; binary or mode-only changes are
skipped with a warning and anchors stay unchecked. Writes `.verdict/state/local-<name>/` (`source.json`, `full.diff`,
`toc.txt`, `roles.txt`) — `source.json` stands in for `PR.json` (kind, base/head
refs+SHAs, range, dirty flag, diff sha256, tool version). The `local-` namespace can
never collide with a numeric PR dir; downstream verbs address the bundle as
`local-<name>`. A bare local slug also resolves: `report owner/repo work` reads
`.verdict/state/local-work/` when no exact `.verdict/state/work/` has a
`PR.json`; `local-<name>` stays canonical. `config` accepts the same local
bundle.

`record owner/repo local-<name>` runs the validate+merge half of `post` with no
POST: hunk index, keep-biased anchor partition, sig merge with append-only history,
resolved/verification/triage claims, writes `findings.json` + `record.json`.
Add `--dry-run` to run every check and write nothing; `--strict` promotes
review-input warnings to errors.

`--cost-names[=<names>]` names the `models[]` entries of the bundle's
`cost.json` and its `chunks/*.cost.json` in place, so spend carries the panel
seat that produced it. `<names>` is a comma-separated list in panel order —
synthesizer first, then each lens; bare `--cost-names` reads the panel the
bundle pinned and refuses when it pins none, or when the bundle's scopes pin
different panels — a cost record does not say which scope wrote it, so pass
the panel that ran. An entry keeps the name of its seat and refuses when a name
sits elsewhere, repeats, or comes from outside the panel; a name is never
invented from a slug. One record's entries are its own seats, so a document
holding several records gives each the whole panel. The pass is planned before
`findings.json` is written and applied after it, so a refusal never leaves
findings behind for `fix` to act on; it changes no token count, keeps each
file's modification time (the
measured-usage join brackets on it), and refuses a rewrite that would grow a
file past the reader's cap. It names the files it already rewrote if a write
fails partway, and with `--dry-run` it proves the repair without writing.
Entries are the array `models` the reader prices, so an object-valued `models`
field or a non-object entry refuses (`cost-models-not-array`) rather than being
converted. Every `record` — including `--dry-run` — also warns once per
`models[]` entry with no `name` (`cost.json: models[0] has no name — the name
is what attribution reads; add "name"`), while leaving the record accepted and
priced; only a missing or non-string name warns, an empty string passes.

`report`/`triage`/`next`/`show`/`verify` work unchanged on local bundles (`report`
reads `head_sha` from `source.json` when `PR.json` is absent). `post` refuses local
bundles (no PR to post to); plain `calibrate` refuses them too (no threads to
settle), while `calibrate --probe` grades them offline — record predictions
locally, settle them after the upgrade below.

Two `post` guards (graded by `evals/cases/post-go-guards.sh`): secondary rate
limits (a 403/429 with a rate-limit signal) retry within
bounds — honoring the `retry-after` hint or the reset marker, at most three
attempts — and review bodies past the 65,536-char
hosting limit split into parts: the first review keeps the footer intact,
and the remaining parts post as follow-up COMMENT reviews with a continuation
header. A failed follow-up refuses typed (`post-failed`) and names the review
that already landed. When the last permitted attempt still carries a
retryable signal, the refusal adds `retry_at` (RFC 3339), so an agent can wait
for that instant instead of parsing the prose.

Upgrade arc: review locally, then `fetch owner/repo N` and
`adopt owner/repo N --from local-<name>`. Adopt copies findings with an `adopted`
history event and re-resolves anchors against the PR hunks (unmatched paths land
`anchored:false`, never dropped), refuses into a non-empty store, and needs a
target verdict can post to (`forgejo`, `github`, `gitlab`) — a host with no submit path
refuses `provider-unimplemented` naming it. From there `post`
and `calibrate` proceed normally — predictions recorded offline get settled against
live ground truth.

### Patch identity (`identity.json`)

Every fetch writes `.verdict/state/<bundle>/identity.json`: the stable diff id beside
the head/base SHAs. The primary algorithm is `git patch-id --stable` over the raw
pre-exclusion diff (`patch_id_kind: "git-patch-id"`); when git is missing or the
spawn fails, the labeled `diff-sha256` fallback (sha256 of the diff minus its
`index` lines) covers the bundle instead, and the two kinds never match each
other. An empty diff carries `patch_id: null` with kind `"none"`, which no guard
matches. The same `patch_id` + `patch_id_kind` are stamped additively into
`source.json`, `post.json`, and `record.json` as provenance. A patch id is a
dedupe key, never an authorization token: it only answers "did we already review
this change", and every use re-binds the stored strings to the bundle's present
diff bytes and head — recomputing the labeled algorithm over the raw input —
before trusting them. An identity whose claim does not reproduce, or whose raw
input cannot be read, is not certified: the run warns and falls back to SHA
behavior instead.

`adopt owner/repo N --from auto` matches the target bundle's verified identity
against `.verdict/state/local-*` twins in the requested repository: exactly one twin
adopts (printed as `from: auto -> local-<name>`), zero refuse `no-patch-match`,
and many refuse `ambiguous-patch-match` naming the candidates (max five shown).
Twins from a foreign repository, with stale bytes, or without a repository record
never match. The literal name `local-auto` stays reachable as `--from local-auto`
(the sentinel is matched first).

`post` refuses `already-reviewed-patch` when a sibling bundle's completed
`post.json` receipt carries the same patch id and kind in the same repository —
unless `--force`, which posts and records one entry per bypassed guard in
`post.json.forced[]` (reason, patch id, prior bundle, prior head), in guard order.
A receipt proves a completed publication only with an identified review in a
completed state (`APPROVED`, `CHANGES_REQUESTED`, `COMMENTED`, `DISMISSED`) and
valid head/time metadata. Incomplete, pending, dry-run, failed, empty-review, or
invalid head/time receipts prove no publication and never block, exactly like an
unposted bundle. A missing, malformed, stale, tampered, unrecomputable, or
unreadable-raw `identity.json` warns `patch identity unavailable — dedupe by SHA
only` and the run falls back to the SHA-only dedupe; a bundle without identity
warns and proceeds the same way.

`report --completeness` passes a rebase or amend that keeps the patch: when the
posted and fetched heads differ but the verified bundle identity and the
completed post receipt share the patch id, the gate passes with `check: same
patch reviewed at <fetched> (posted <posted>) — completeness passes
(rebase/amend)`. A different patch keeps today's stale-head failure, and a bundle
without verifiable identity warns `patch identity unavailable — completeness
compares heads` and compares heads only.

### Isolated checkout (`--worktree`)

`fetch-local --worktree` checks the review target into a temporary detached
worktree under `.verdict/state/` and builds the bundle from that checkout instead of
the live tree: the default dirty tree via a read-only stash snapshot, `--staged`
via a synthesized index commit, `--range`/`--base`/`--stash` at their head
commits. The bundle bytes match the classic run on a quiet tree, but a dirty
working tree — yours or a teammate's editor saving mid-fetch — cannot leak in
or tear the read. The run prints the checkout path (`worktree: ...`) and always
removes it before exiting, on success and on every refusal or cancel; a removal
that fails warns with the manual `git worktree remove --force` recovery. When
no checkout can be created the verb refuses typed `worktree-failed` and writes
nothing. Re-materialize the same tree later for caller tracing or `verify`
runs with `git worktree add --detach /tmp/<name> <head-sha from source.json>`
and remove it when done — the working tree stays untouched throughout.

### Fix loops (bounded, verify-gated)

`fix owner/repo local-<name> [--sig <s>] [--json]` runs one bounded pass over a
local bundle: it picks the top open inline sig (severity `critical` above
`important` above `informational`, then path) or the `--sig` prefix (must be open
and inline; non-open, section, and unknown sigs refuse), prints the finding
(plus its stored `brief` block when one is present and the full `kb.txt` context
when present) with patch → verify → land → re-run instructions, and records the
pass in `.verdict/state/local-<name>/fix.json`. The agent patches the anchor file,
runs the verify command itself, writes `verification.json`, lands via `record`,
and re-runs `fix`; the loop ends when no open inline sigs remain (completion
summary with fixed/open/stopped counts plus the ledger path). A ceiling-exhausted
top sig keeps getting picked — pass `--sig` for another sig or triage the stuck
one. Ceilings are
hard-coded, not configurable: max 3 passes per sig (the 4th refuses with
`fix-ceiling`), and touching any worktree file outside the anchor stops the loop
(`scope-escape`) — an effective files ceiling of 1. Pre-existing worktree dirt is
grandfathered (only files changed since the first pass can trip the audit).
`fix` never flips status or
writes `findings.json` (credit still flows through the normal `record` flips);
PR/provider bundles refuse with `not-local`. Cost note: every pass is another
agent run over the bundle — loops multiply agent spend, so set
`REVIEW_BUDGET_USD` (or the typed `budget_usd` default) before looping a large backlog. Ceilings
constrain a cooperating driver, not an adversary (deleting `fix.json` restarts
counts) — the ledger is an audit trail, not a lock. Verifying an untrusted
worktree? `verify` refuses hostile shapes pre-exec, but the static gate cannot
see everything — run the verify step under the Hostile-PR sandbox recipe below.

## Hostile-PR sandbox recipe (enforced on Linux with --sandbox; recipe for other OSes)

When the diff is untrusted (unknown author, drive-by PR, patch claims
arriving via `fix-ci`) and the `verifyCmd` wants the network, run the verify
step — not the whole review — inside a throwaway sandbox so a malicious
command finds no credentials and no route out. The `verify` verb already
refuses hostile shapes before spawning anything — destructive
(`verify-destructive`), networked (`verify-networked`), credential-reading
(`verify-credential-read`), repo-escaping (`verify-repo-escape`) — each
typed refusal quoting the matched fragment with a `try:` rewrite hint, and
the refusal leaves `patch.json` and `.verify.out` untouched. The sandbox
below is the second layer for what the static gate cannot see (a plain
`curl` GET stays allowed; `/tmp` scratch writes stay allowed) — not a
replacement.

### Enforced sandbox: `verify --sandbox`

On Linux with `bubblewrap` installed, pass the flag and let `verify` build
the sandbox itself:

```bash
verdict verify owner/repo 123 --sandbox
```

It runs the command (patch `verifyCmd`, or the deep-group
`--group ... --command ...` pair) under `bwrap` with `--ro-bind / /`, a
writable bind of the reviewed root, a private `/tmp`, an empty `/run`, a
fresh `/dev` and `/proc` under `--unshare-pid`, `--unshare-net`,
`--new-session`, and `--die-with-parent`. The command gets its own loopback,
its own PID namespace, and no route out:

- **No host process data.** `/proc/<host-pid>` does not exist, so the
  environment of the parent `verdict` process and every other same-uid
  process is unreadable.
- **No unix-socket route.** `/run` is empty, so `connect()` on
  `/run/docker.sock`, `/run/user/<uid>/bus`, or another agent socket fails.
  `--unshare-net` alone does not cover unix sockets.
- **No terminal injection.** `--new-session` starts the command in a new
  session, blocking TIOCSTI; `--die-with-parent` kills the sandbox when
  `verdict` dies.

Each credential path below, when it exists on the host, is replaced before
the command runs:
directories (`~/.aws`, `~/.ssh`, `~/.config/gh`, `~/.docker`, `~/.kube`,
`~/.config/gcloud`, `~/.azure`, `~/.gnupg`) with an empty `--tmpfs`, and
files (`~/.netrc`, `~/.git-credentials`, `~/.npmrc`, `~/.pypirc`) with a
read-only bind of `/dev/null`. A missing entry adds no mount. `/var/run` is
shadowed too when the host ships it as a real directory; on the distros
where it is a symlink to `/run`, the `/run` mount covers it.

Before anything executes it probes the same flags with a no-op; when the
host is not Linux, `bwrap` is missing, the user-namespace restriction is on,
or `HOME` is unset, `verify` refuses `sandbox-unavailable` naming the cause
and runs nothing. It never silently falls back to an unsandboxed command.
The patch record and the deep-group receipt carry `"sandboxed": true`, and
the `--json` data adds `"sandboxed": true`; absent means the run was not
sandboxed. The same opt-in reaches the MCP tool (`sandbox`), the Go facade
(`Sandbox`), and the TypeScript client (raw `--sandbox` argv).

### Recipe for other OSes (and for wrapping the whole review)

The one-liner below still applies on macOS or a minimal image with no
`bwrap`, or when the whole `verdict` process must run inside the sandbox:

`bwrap` one-liner, from the repo root (`verify` runs the same `verifyCmd`;
only the filesystem and network it sees change):

```bash
bwrap --ro-bind / / --dev /dev --proc /proc --unshare-pid --tmpfs /tmp \
  --tmpfs /run --bind "$(pwd)" "$(pwd)" \
  --tmpfs ~/.aws --tmpfs ~/.ssh --tmpfs ~/.config/gh \
  --ro-bind /dev/null ~/.netrc --new-session --die-with-parent \
  -- verdict verify owner/repo 123
```

`--ro-bind / /` makes the host read-only, `--bind` remounts the repo
writable over it, `--dev` and `--proc` under `--unshare-pid` replace the host
device and process trees, `--tmpfs /run` removes the unix-socket routes, and
each `--tmpfs` hides a credential dir behind an empty mount while
`--ro-bind /dev/null` replaces a credential file. Add the rest of the
credential table from above (`~/.docker`, `~/.kube`, `~/.config/gcloud`,
`~/.azure`, `~/.gnupg`, `~/.git-credentials`, `~/.npmrc`, `~/.pypirc`) and
drop the mounts for paths this host does not have. Add `--unshare-net` when
the verify step needs no network at all — full network isolation for the
command under test:

```bash
bwrap --ro-bind / / --dev /dev --proc /proc --unshare-pid --tmpfs /tmp \
  --tmpfs /run --bind "$(pwd)" "$(pwd)" \
  --tmpfs ~/.aws --tmpfs ~/.ssh --tmpfs ~/.config/gh \
  --ro-bind /dev/null ~/.netrc --unshare-net --new-session --die-with-parent \
  -- verdict verify owner/repo 123
```

No `bwrap` (macOS, minimal images)? `systemd-nspawn` covers the network
half of the shape only — network-only isolation, not the same shape:

```bash
sudo systemd-nspawn --directory=/ --bind="$(pwd):$(pwd)" --private-network \
  -- verdict verify owner/repo 123
```

`--directory=/` runs the container on the host rootfs, so this is
network-only isolation: `--private-network` removes the route out, but the
command still sees the host filesystem and credential paths. Prefer the
`bwrap` variant above whenever filesystem or credential hiding matters.

That one-liner stays a recipe, not enforcement — only `--sandbox` probes and
refuses. Per K5, full namespace isolation stays opt-in, never default. The
gate constrains what `verify` will execute; your own shell is unaffected
either way.

### Known residuals (string-scan ceiling)

The gate stops every literal shape in its contract; the shapes below need
the sandbox recipe above, not another string arm. Tripwire: a real incident
through any of these reopens the mandate discussion.

- file-content weapons (requirements URLs, setup.py/build.rs/extconf/postinstall/
  Makefile/test-file code, registry configs, `@file` response files);
- module-resolution paths (`PYTHONPATH`, `NODE_PATH`, `PERL5LIB`, `RUBYLIB`,
  `GEM_HOME`, `CARGO_HOME`, …) including innocent-command hijack;
- env-directed fetch destinations (index/registry envs; registry _names_ have no URL
  to key on);
- dynamic construction (`$'…'`-hex, base64, variable indirection, alias-`!`);
- interpreter-mediated execution (`python|perl|ruby|node|php …`, `curl | python3`);
- fetch-then-exec splits across the `/tmp` carve-out (I3);
- TOCTOU/meaning-change (symlink swaps, concurrent-run shared state);
- unlisted clients/wrappers (the next `gh` verb, the next pipe wrapper — report-and-arm
  as found, never "complete");
- container/VM escape (out of scope by recipe, not by gate).

## Child environment (what inherits, what is explicit)

Documented so nobody "fixes" it: verdict's children do not all get the same
environment, and the split is deliberate (the security rationale is in
`SECURITY.md` § Child process environment).

- The `gh` and `git` calls **verdict issues itself** (fetch, post, triage,
  `doctor`, and the base-owned control reads) **inherit your environment**.
  That is the point: they must use your `gh` auth and your git config,
  because verdict is a local CLI working on your behalf.
- The **agent-written `verifyCmd`** runs under `bash -c` with an **explicit**
  environment — the ordered snapshot the CLI hands `verify`, not a silent
  inheritance. The same snapshot is the redaction list for the recorded tail,
  so a token you exported is scrubbed from `.verify.out` and `patch.json`.
- The **agent `run` launches** inherits your environment for the same reason
  `gh` and `git` do: it authenticates with your key, on your machine. `--json`
  adds `NO_COLOR=1`. The recorded tail in `launcher.json` scrubs the values of
  your secret-named variables and the argument secrets the argv redaction
  hides, so the argv and the tail agree about what a credential is. `.run.out`
  is the child's raw stream — it may hold a credential the child printed — so
  it is written `0600` on Unix, the way `.verify.out` keeps its raw stream.
  Windows has no mode bits for Go to set, so there it takes the directory's
  inherited ACL and the owner-only guarantee does not apply.

In tests the counterpart is `internal/testenv`: every harness that spawns
`git`, `gh`, or `verdict` builds a fixed, secret-free child environment
(secret-shaped names, harness knobs, and git's hook-exported location
variables dropped; git configuration and identity pinned) and never inherits
the runner's. Production inherits the operator's environment on purpose; tests
never do.

## Over-cap diffs (chunked fetch + manual recipe)

Diffs over 512000 bytes refuse with typed `diff-too-large`: the refusal names
the size, the cap, and the split-or-narrow escape, and carries advisory split
candidates (largest file alone, everything-but-largest, range half-split) —
nothing auto-narrows. Deliberate exceptions only: `REVIEW_MAX_BYTES=<n>` env
or the typed `max_bytes` default in `.verdict/config.json` (env wins, then the
typed default, then 512000; either override disables chunking outright and the
fetch prints its source). The typed cap and `chunk` default are read from the PR
base commit, so a PR cannot raise its own cap.

Chunked fetch is the opted-in over-cap path: pass `--chunk` on `fetch` or
`fetch-local`, or set the typed `chunk` default to `true`. The flag wins over the
typed default; absent means off and refuses byte-identically to today.
Under-cap fetches succeed with identical bytes whether chunking is on or not
(the opt-in is never consulted under the cap).

An opted-in over-cap fetch is machine-sliced on file boundaries (greedy
next-fit in diff order, deterministic — re-fetch reproduces the map):
`<dir>/chunks/` holds `chunk-NN.diff` (each at or under the cap, concatenating
byte-identically to `full.diff`), `chunk-NN.toc.txt` (per-chunk coverage
checklist), and `chunk-NN.roles.txt`, plus `chunks/manifest.json` (`schema_version
"1"`, fetched HEAD/base SHAs, cap bytes + source, total bytes, and the ordered
chunk list with member files, sizes, and diff/toc/roles refs). The top-level
bundle keeps `full.diff` for anchor pinning and drift detection; the `--json`
envelope gains additive `data.chunk` (`count`/`manifest`/`total_bytes`);
`--show-prompts` fences the chunk diffs in manifest order (one ordered
document, byte-equal to `full.diff`, labeled `source=full.diff`).

Review from the chunks: read the manifest first, then each chunk diff
end-to-end against its own checklist — an unread chunk is as visible as an
unread TOC range (agent contract in `prompts/review.md`; delta re-reviews
scope to the affected chunks per `prompts/incremental-review.md`). Synthesize
exactly one `review.json` across all chunks — same body format, callout
ladder, and evidence-rung tags as a small-PR review, rungs summed across
chunks into the one Basis line — and stamp top-level `"chunks": [...]` naming
every manifest chunk covered. `post`/`record` refuse attested-but-partial
coverage (`missing-chunk-coverage`) or unreadable snapshots
(`missing-chunk-snapshot`), each naming the `fetch ... --chunk` re-fetch, so
exactly one review posts, exactly once. Reviews without the claim validate as
today (delta re-reviews, single-anchor probes) with a loud warning naming the
manifest — the never-posts-partial guarantee holds only for attested
reviews, so every full synthesis must stamp the claim. Cost is one `cost.json` total
rolling up every model in the run; `report --cost` additionally folds in
per-chunk `chunks/<name>.cost.json` records when present.

One file larger than the cap cannot be file-sliced: that degenerate input
keeps the advisory refusal with no chunks written. The manual hand-slice
recipe stays valid for opted-out and degenerate inputs alike — hand-slice
it (split the range and re-fetch each slice, or narrow with a paths filter),
then review each slice and stitch the verdict yourself:

```bash
verdict fetch-local owner/repo slice-a --range <base>..<mid>   # or --base <R>
verdict fetch owner/repo 123 --base-sha <sha>                  # or --since <sha>
verdict fetch-local owner/repo slice-b -- <paths...>           # narrow to a file subset
```

## Providers (non-GitHub hosts)

`fetch --provider gitlab|gitcafe|forgejo|bitbucket|azure` builds the same bundle
shape from another host (`PR.json` normalized to one schema: `provider`,
`number/title/body`, `base_ref/head_ref`, `base_sha/head_sha`, `author`, `url`,
`state` as open|closed|merged, `draft`, per-file `additions/deletions`).
`write_toc_roles` and the whole validate+merge path run unchanged — anchors
validate against `full.diff` hunks regardless of host. `record`, `report`,
`triage`, `show`, `next` work on provider bundles as-is. `post` submits to
GitHub, GitLab, and Forgejo; `ready` advises on GitHub and GitLab, plain
`calibrate` joins live threads on both, and `adopt` merges into any bundle
verdict can post to (its re-anchor reads the stored hunks and never shells a
host CLI). `post` on `gitcafe`, `bitbucket`, and `azure`, and `ready` and plain
`calibrate` on any host but GitHub and GitLab, refuse typed, while `adopt` into
a host without a submit path refuses `provider-unimplemented` naming the host,
and `calibrate --probe` grades any bundle offline. The refusal names the gap
rather than an impossibility: each remaining host still needs its own submit
path, with the comments → summary body → verdict-last ordering laid out below.
`fetch --json` publishes the facts in
`data.capabilities`: `provider`, `fetch`, `post`, `inline_anchors`, `approve`,
`request_changes`, `thread_resolution`, `review_verdicts`, and `unsupported`,
so an agent can plan before it writes a review. `approve` and
`request_changes` split the binding verdicts because hosts differ: GitHub
carries both, while GitLab approves a merge request but has no request-changes
review event, so a bound request-changes posts the discussions plus the summary
note and warns that the verdict did not bind. `review_verdicts` stays as the
compatibility sum `approve && request_changes` for consumers written before the
split, and carries no reason of its own — the two flags explain it; new readers
branch on those two flags. `thread_resolution` is true on GitHub and GitLab:
`threads resolve` closes a verdict-authored thread through `resolveReviewThread`
or GitLab's discussion resolved flag.
Forgejo has no documented resolve endpoint, so it reads false there and on
every fetch-only host; resolution stays a host-CLI step the prompts hand back
to the agent. `unsupported` carries
one machine-readable code per absent capability, keyed by capability name:

| Code                       | Meaning                                                                         |
| -------------------------- | ------------------------------------------------------------------------------- |
| `fetch-only`               | no submit path for the host, so no post, anchors, or binding verdicts           |
| `no-request-changes-event` | the host has no request-changes review event; the post degrades, with a warning |
| `no-approve-verdict`       | the host posts but cannot record an approval                                    |
| `no-inline-anchors`        | the host takes a body but no anchored comment                                   |
| `no-resolve-mutation`      | verdict has no thread-resolve call for this host                                |
| `unknown-host`             | the provider is not one verdict reads                                           |

| Provider    | Fetch reads                                                                                                                                                              | Auth                                                             | Gaps (v1)                                                                                                  |
| ----------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ---------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------- |
| `gitlab`    | `glab mr view/diff/note list/discussions` + changes/compare API; `ready` adds the MR, pipelines, and discussions reads, `calibrate` the discussions + repository compare | `glab auth login` (or `GITLAB_TOKEN`)                            | experimental note commands; incremental via repository compare; `post` approves but cannot request changes |
| `gitcafe`   | `cafe pr view/diff/reviews/comments`                                                                                                                                     | `cafe auth login` (or `CAFE_TOKEN`)                              | reads `.data.*` envelope; threads paged via `--cursor`; hunks render only when `.patch` text ships         |
| `forgejo`   | REST `curl` (`$FORGEJO_HOST/api/v1`, `$FORGEJO_TOKEN`) — neither `fj` nor `tea` exposes JSON review reads                                                                | token env                                                        | inline threads aggregated per review id; a review comment anchors on a file line number                    |
| `bitbucket` | REST `curl` (`$BITBUCKET_TOKEN` Bearer or user+password Basic)                                                                                                           | token env                                                        | diffstat/comments paginated server-side; no grouped submit                                                 |
| `azure`     | `az repos pr show` + `az rest` threads/iterations; diff via temp-clone `git fetch` of the two SHAs                                                                       | `az login` / `AZURE_DEVOPS_EXT_PAT`, `AZURE_DEVOPS_ORG` required | needs git reachability to the repo remote; no org/project auto-detect                                      |

Neither `curl` host takes its credential on the command line, where `ps` and
argv logs would show it: the client reads a one-directive config document from
stdin (`--config -`) on a fetch read, and from a `0600` file in a private temp
directory — removed after the call — on the Forgejo review write, whose stdin
already carries the JSON body. A token or password carrying CR or LF refuses
`auth-missing` before `curl` starts, because that document is line-oriented and
a break could end the directive and start another.

State maps to open|closed|merged at fetch (`opened`→open, `active`→open,
`ABANDONED`→closed, `SUPERSEDED`→closed, `completed`/`merged`→merged;
anything else lands `unknown`, and `post` refuses any non-open PR
(`pr-not-open`) before it submits). File +/- stats come from the host API where it
provides them (GitCafe, Forgejo, Bitbucket); otherwise they are derived
from the reviewed diff itself (`git apply --numstat`), so the coverage
manifest always describes the fetched object.

Incremental re-review (`fetch --base-sha` / `--since`) runs on GitHub and
GitLab. On GitLab the delta is `glab api
projects/<id>/repository/compare?from=<base>&to=<head>`: the response's
per-file `diff` entries join into `incremental.diff` under the same
`diff --git` headers the GitHub compare path writes. A failed compare still
finishes the fetch — it writes `incremental.diff.note` beside the absent
delta and keeps `glab`'s stderr in `.compare.err`, exactly the GitHub
posture. Hosts with no compare adapter (GitCafe, Forgejo, Bitbucket, Azure)
accept the flag but write the note naming the unsupported provider instead
of a delta; the fetch never fails for it. The delta then feeds the ordinary
record/advance path: `record` reopens an addressed finding whose path the
delta touches, stamps the reopen history with the fetched head, and writes
`record.json` at that head; `post` names the next `--base-sha` advance.

A host that advertises `inline_anchors: false` is sent no anchored comment at
all: `post` hands that answer to the merge, which keeps every finding and posts
it in the body instead of a position the host would refuse, warns which host
did it, and counts the findings in `anchors.moved`.

`post.json` records the same gaps for the post that actually landed, because
the envelope is not kept beside the bundle. `withheld` carries one entry per
capability the host could not carry, using the capability names and reason
codes above:

```json
"withheld": [
  { "capability": "request_changes", "reason": "no-request-changes-event", "requested_event": "REQUEST_CHANGES" }
]
```

That entry is what separates the two GitLab receipts that otherwise look
alike: a bound request-changes the host cannot record leaves `review.state`
reading `commented`, exactly as a plain comment does, and only `withheld` says
which one posted. The key is absent when the host carried everything the run
asked for, so an undegraded receipt keeps its bytes; `requested_event` is
absent for a capability with no event of its own (`inline_anchors`, whose loss
the `anchors` tally counts).

### Submit (`post`) per host

GitHub and Forgejo each take one atomic review; GitLab has no such call, so its
submit replays three primitives in that order. GitLab posts the summary body
last, and no host replays a payload whose first write landed: a mid-way failure
refuses typed `post-failed`, names what landed in the envelope `warnings`, and
writes the host's own stderr to `.post.err`. The guarantee covers positioned
discussions. On GitLab a review with no comments posts only the summary note,
and a summary write whose response was lost can be retried because nothing
landed to observe; that case is the same lost-response shape GitHub documents.

| Provider  | Submit writes, in order                                                                                                                                                                                                                                                                            | Gaps                                                                                                                                                                                          |
| --------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `github`  | one `repos/<owner>/<repo>/pulls/<n>/reviews` POST carrying `body`, `event`, and every inline comment (`gh api … --method POST --input -`)                                                                                                                                                          | —                                                                                                                                                                                             |
| `gitlab`  | one MR discussion per finding — a `position` with the MR's `base_sha`/`start_sha`/`head_sha`, both paths, `new_line` for an added line, both numbers for an unchanged one, `old_line` for a deletion — then one summary note, then `approve` (pinned by `sha`) only when the review is an approval | approve-only (GitLab has no `request-changes` event — the comments still post, with a warning); a multi-line anchor lands on its end line; `request_changes` and `review_verdicts` stay false |
| `forgejo` | one `api/v1/repos/<owner>/<repo>/pulls/<n>/reviews` POST over `curl` carrying `body`, `event` (`APPROVED` for an approval), `commit_id`, and every comment (`new_position` or `old_position`, both file line numbers)                                                                              | —                                                                                                                                                                                             |

Forgejo has no CLI, so both directions are one `curl` call against
`$FORGEJO_HOST/api/v1`; the write is GitHub-shaped — one atomic review POST
carrying the body, the event, the reviewed commit, and every comment — but three
limits need saying. Forgejo spells the approval `APPROVED`, and an event it does
not know creates a **PENDING** draft with a 200, so `post` maps the event and
refuses any answer whose `state` is not the one it asked for: a pending review
is a draft nobody else sees. A review comment anchors on a **file line number**,
not a diff offset, so a RIGHT anchor becomes `new_position` and a LEFT anchor
`old_position`. A finding with no numeric line is withheld (Forgejo would accept
an unanchored comment), a LEFT anchor on a path the change deletes is withheld
(old-side blame cannot resolve there), and a multi-line range lands on its end
line — each loss is counted in the envelope `warnings`. Before the write, `post`
reads the token user and the PR's reviews: a `PENDING` review authored by that
user refuses with a submit-or-discard hint, because Forgejo would attach these
comments to that draft and submit the whole thing under this review's body. An
unreadable user read refuses before the write too, because the draft could not
be ruled out.
Nothing on the wire is atomic on the server, so a failed write is never
replayed: the warnings say how many comments may have landed. Two limits stay
unverified: whether a left-side anchor on a deleted file resolves at all — `post`
withholds it — and whether the review list returns another user's draft. The
draft check reads that list and refuses when the read breaks, but a host that
filters other users' drafts out of the list would hide one.

The repeat guard is the same on every host: the body's
`<!-- verdict-review payload=… -->` marker is read from the fetched
`reviews.json`, so a re-fetch restores it after a post, and the marker keys on
the exact payload — body, anchors, and reviewed head — never on a diff
position, which shifts as the branch gains commits.

## Calibration (predictions vs ground truth)

Everything the store records is a prediction; `calibrate <owner/repo> <pr>` checks it.
Read-only join (threads + one compare + PR state), keep-biased:

- Sources: live review threads (GraphQL on GitHub, the discussions list on
  GitLab, matched by the exact body we sent — robust, no
  fuzzy matching) → per-finding outcome; `post.json:review.id` records which review posted them;
  one `compare` of review head vs live head for changed-file evidence (the
  repository compare read on GitLab); PR merged state as context. Terminal findings are
  skipped, not judged.
- Outcomes: `fixed` (cited file changed + thread resolved), `resolved-unfixed` (resolved
  without touching the cited file — disagreement or fix-elsewhere), `open` (still open),
  `unknown` (comment not found, review deleted, or ambiguous — never judged).
- Rules: resolved ≠ fixed; `unknown` never counts against precision; merged-with-open is
  recorded, not judged; tier precision reported separately — a wrong `critical` costs more
  than a wrong `informational`.
- Use: per-model verdict calibration over time (which model judges well feeds the panel file).
  Never auto-tunes thresholds — humans read the report, the script only joins.
- Enabler: `post` records `.verdict/state/N/post.json` (`review` id, `head`, `posted_at`)
  on every real post. A plain `calibrate` refuses without it; `--probe` works offline.

The offline counterpart is the self-grading probe (X3): `calibrate
<owner/repo> <pr> --probe` writes one row per finding to
`.verdict/state/<bundle>/kb-grades.jsonl`, each row carrying two separable parts. The
script-owned baseline grades every finding against its triage disposition
(`addressed` = real, `false-positive` = false, anything else = `unknown`,
never forced) with the first cited rule and its helpfulness (`true` on real,
`false` on false alarms, `null` otherwise) — regenerated from `findings.json`
on every run. The agent judgment loop records sampled re-reads of closed
findings against the code in `agent_verdict` / `agent_helpful` columns that
MAY diverge from the baseline, merged machine-readably via `--judge
SIG:VERDICT[:HELPFUL]` or `--judge-file PATH` (every sig validated against
the findings store; unknown sigs refuse typed), with `agreement` computed per
row. The evidence number is the agreement rate over doubly-judged rows
with the sample size recorded alongside. The run is additive-only (that one
file; findings, joins, and calibration outputs stay byte-identical),
deterministic and idempotent, and needs no post record and no network —
provider and local bundles both grade; the probe runs before the live-thread
path. The agent side lives in `prompts/grade.md`: sample closed
findings, re-read the code, write judgments — advisory prose supplements the
record, it is not the record.

## Trend (portfolio outcomes over time)

`trend <owner/repo>` rolls up every reviewed bundle (`.verdict/state/*/findings.json`)
into one time series: findings opened/addressed/closed per review date, broken
down by severity tier and status, plus per-bundle rows (review time, models and
effort from `cost.json` when the agent wrote one, provenance from the post
receipt else the calibration record — `tool_version`, `prompts_hash`, `model`,
missing reads unmarked). Pure read — no network,
nothing written, recomputed every run so hand-edited stores stay truthful;
missing or unreadable stores warn and skip, and no reviewed bundles reads as
zeros, never an error. `closed` means no longer actionable
(`wont-fix`/`false-positive`/`deferred`); null severity reads `untagged`,
mirroring `report --check`; counts are per store, so adopted findings appear in
both the local and the PR bundle. Feed for the calibration narrative: per-bundle
models plus per-tier outcomes is the raw material for per-model tier precision
over time — run `calibrate` on recent bundles to settle what the trend only counts.

`trend --overlap` adds the KB overlap audit to the same rollup: an offline join
of `kb-recall.json` manifests, findings `kb` cites, and `.verdict/kb-joins.jsonl`
into the three arena-binding numbers — recall precision (recalled rules actually
cited), recall coverage of cites (cited rules that came from recall rather than
invention), and the cited-finding outcome split (`addressed` vs `false-positive`,
with `open`/`deferred` pending) — plus per-bundle rows carrying each bundle's
own precision/coverage/split. Missing or unreadable manifests read as unmeasured
(never zero) and stay out of the precision/coverage denominators; the outcome
split still counts cited findings from unmeasured bundles. Joins rows corroborate
credited flips, and per-bundle `calibrate_eligible` marks bundles carrying a
posted review id + head that `calibrate` could settle against live threads — the
audit reads those shapes only, never recomputes them and never touches the
network. Without the flag both outputs are byte-identical to today.

## Code index (Go)

`index build [--from <dir>] [--at <ref>]` parses a git tree with the standard
library (`go/parser`, `go/ast`) — no tree-sitter, no cgo, no third-party
module — and writes a per-tree index under `.verdict/index/`, which the tracked
ignore contract keeps local. The query key is the tree SHA, so an empty commit
reuses the same tree file; shards are content-addressed by blob, so a pure
rename parses nothing and an edit parses one file. `--from` reads another
checkout; `--at` names a ref (default `HEAD`).

`index status` reports the stored trees and shards and whether HEAD's tree is
indexed. `index prune` keeps the newest four trees and the shards they
reference. `index callers <symbol>` answers call sites for one symbol: results
are **exact within a package** (object identity in that package's declarations)
and **lexical across packages** (an exported name reached through an import
edge); otherwise they are counted in `counts.unresolved`, never guessed.
Methods, interface and dynamic dispatch, reflection, and generic instantiation
are unresolved by construction. A bare symbol (`Target`) selects every package
declaring it; a qualified one (`app.Target`, or the import path) names the
package. A qualifier that matches no package directory or import path refuses
`bad-flags` naming it, as does an ambiguous one, so a typo never reads as no
callers. Rows sort by path, line, and kind, and truncate to `--limit` (default
50, maximum 200) with `data.truncated` naming how many were not shown.

The honest limits are the contract: **exact within a package, lexical across
packages, incomplete by construction**. Do not read a caller row as proof of
every call: a method value, a function stored in a variable, or a type argument
is counted unresolved, not found.

`index tests-for <path>|--symbol <pkg.Name> [--at <tree>]` lists the candidate
tests to read for one source file (or one qualified symbol), each labelled with
the mechanism that reached it: `same-package` (a `_test.go` in the package
directory), `references` (the test's recorded references name a symbol declared
in the file, resolved exactly within the package and lexically only through an
import edge), or `imports-module` (the test imports the declaring package).
Only `_test.go` files appear. These are **candidates, never coverage claims**:
a row names a test worth reading, not a test that covers the change, and a test
the index cannot attribute (a method value, a reflection call) is missing
rather than guessed. Rows sort by path and truncate to `--limit` with a count.
A path that names no indexed Go file refuses `bad-flags`, and `--symbol` wants
a qualified `pkg.Name` — a bare name would fold unrelated packages together.
The path form also carries the file's CODEOWNERS `target`.

`index co-changed <path> [--commits <n>] [--since <dur|ref>] [--min-count <n>]
[--limit <n>]` reads one bounded `git log --no-merges --name-only -z` window
(default 2,000 commits) and reports every path that moved in the same commits,
with its co-change count and Jaccard ratio `|A∩B|/|A∪B|` over the window,
rounded to four decimals. Rows sort by count then path; partners below
`--min-count` are dropped. The window is echoed — oldest and newest commit,
commit count, and how many commits touched the target — so a count is never
divorced from its denominator. Merges are excluded. The call passes
`--no-renames` under a pinned `diff.renames=false`, so a rename counts as its
old and new path in one commit — both names co-change with everything in it
and the two are never joined here — and no repository diff config can shrink
the window. The history read is pinned to the commit the query resolved, so a
ref that moves mid-query cannot mix two windows. `--since` takes a duration
(`90d`, `12w`,
`18mo`, `2y`) measured back from HEAD's own committer date, or a ref whose
history is excluded. A shallow clone warns that the counts are true of the
commits present only. The window is cached under
`.verdict/index/cochange/<head>-<commits>.json` (two newest kept); the file is
timestamp-free, so the same window writes the same bytes, and a corrupt cache
is rebuilt rather than read as an empty window. The cache is best-effort: a
query writes nothing into an absent index store, nothing for a shallow clone's
window (a truncated window must not survive `git fetch --unshallow`),
nothing for a window holding a path that is not valid UTF-8 (JSON would rewrite
the bytes and a later hit would stop matching), and nothing through a symlinked
`.verdict/index`, `cochange/` directory, or cache file — a write or a prune
must stay inside the checkout. Each costs the answer nothing —
the rows are already computed — and one
`» index: co-change cache not written: <reason>` line on the human surface
names why nothing was cached; a shallow clone's own warning carries its case.
The CLI keeps that cache; an MCP query reads a valid window if one is present
and never writes or prunes one.

`index owners <path> [--at <tree>]` answers the CODEOWNERS rule that owns one
path at the queried tree. The file is read **at the tree**, from the git object
store, at GitHub's first match of
`.github/CODEOWNERS`, `CODEOWNERS`, then `docs/CODEOWNERS` — never the worktree
copy, so an uncommitted edit changes nothing. The last matching rule wins, and
a rule with no owners is kept: it clears an earlier match, so the path reads
`owners: []` with the source named — GitHub's way of saying the last matching
rule removed ownership. An inline `#` starts a comment, so `*.go @a # contact`
names only `@a`; `\#` inside a pattern writes a literal `#`, while a line that
starts with `\#` is not a pattern at all — GitHub reads it as a comment, so
the rule matches nothing and joins the warning below. Patterns follow
gitignore's positional subset: `*` stays inside a segment, `?` matches one
character, `**` crosses segments only in the three positional forms `**/`,
`/**`, and `/**/` and is a plain `*` anywhere else (so `a**b` and `a**/b`
never match across a `/`), a leading `/` anchors at the repository root, and a
pattern with no slash matches a segment at any depth. A trailing slash matches
everything below the directory; a pattern without one matches a file or
directory by that name, so a directory rule also owns every file below it
(`**/logs` owns `build/logs/error.log`). A final segment that is a wildcard
names files only: `docs/*` owns `docs/a.md` and never `docs/a/b.md`. Negation
(`!`), every other backslash escape, and character classes are not evaluated:
a file using them warns, and those lines match nothing. A tree with no
CODEOWNERS reads `owners: null` and `owners_source: null`; a file no rule
matches reads `owners: null` with the source named; a matching ownerless rule
reads `owners: []` with the source named. None ever reads as an owner named
nobody. The same `target` object rides on `tests-for` (path form) and
`co-changed`.

Caps: 20,000 Go files, 1 MiB per file (an oversized file is skipped and
counted in `coverage.skipped`), a 96 MiB tree file, and a 256 MiB shard cache;
a repo over a cap refuses `index-capped` naming the measured number. The typed
`index` section of `.verdict/config.json` overrides each default. The 1 MiB
file cap governs Go parsing alone: a CODEOWNERS blob is read whole from the
object store, so a large ownership file is never silently half-applied. That
read has no size limit of its own; GitHub loads no CODEOWNERS file of 3 MB or
more, so past that limit the answer names the owners the file's rules describe
and GitHub requests none of them. One build reads every parseable blob through
a single `git cat-file` capture capped at 97 MiB including record framing, so
the total source size parsed in one build is the effective ceiling. It binds
before the nominal 20,000 × 1 MiB file caps; an over-cap capture refuses
`index-capped` naming the cap, and the capture cap is not configurable. A
truncated or invalid shard refuses `index-corrupt` and is never read as empty.

Querying a tree that is not HEAD's marks the envelope `stale: true` and warns
`» index: built for <tree>, HEAD is <tree2>`. `callers`, `tests-for`, and
`owners` take `--at`; `co-changed` always reads HEAD's tree and history. The
MCP `index` tool is read-only and serves `callers`, `status`, `tests-for`,
`co-changed`, and `owners`; it creates no workspace and no index store, writes
nothing (a `co-changed` query reads a valid cache but never writes or prunes
one), and it never builds or prunes — those stay on the CLI, and the SDK
forwards every subcommand.

## Backtest (SZZ candidates)

`backtest mine --since <90d|12w|18mo|2y|ref> [--until <ref>] [--run <slug>] [--json]`
mines bug-introducing PR candidates from the local clone. It makes zero model
calls and zero network calls: everything comes from git objects already on
disk. `replay` (run the review) and `score` (join findings to the later fix)
are later slices; this release ships `mine` alone.

Every mining git call runs with the system and user config nulled — the artifact
never depends on a formatting knob — with one exception: the user's
`safe.directory` list, read from the `--system` and `--global` scopes with the
user's own git environment and forwarded at command scope, so a checkout owned
by another uid mines exactly when that user's own git trusts it.

A **candidate** is one merged PR plus the lines a later fix deleted that blame
to that PR's head. It is a hypothesis about where a defect was introduced —
not a defect, not a finding, not a calibration result, and not citable. SZZ
blame is noisy: a fix can touch cleanup adjacent to the real bug, a refactor
can move a line without changing it, and a squash commit is the whole PR only
by convention. A candidate can be an intra-PR self-fix: a fix commit inside a
merged PR's branch is a seed like any other, so the PR a candidate names may
be the same PR that carried the deleting commit. The anchor runs at the PR
head, where that line is usually already gone, so most of these read
`unaligned`; when an identical line survives at the head, the candidate can
still name the PR. Treat `mine.json` as the input to replay, never as
evidence.

### Window

`--until` defaults to `HEAD`; any other value must resolve to a commit in
`HEAD`'s history, else `bad-until`. `--since` is either a calendar span (`90d`,
`12w`, `18mo`, `2y`) measured back from `--until`'s committer date, or a ref
that must be an ancestor of `--until`, else `bad-since`. A bare `m` is refused
— months are `mo`, minutes do not exist. The range is `since..until`; when no
commit predates the cutoff, the span starts at the history root. A range too
large to list in one capture refuses `window-too-large`, naming `--since`:
narrow the span. In a shallow clone the run warns that an introducer at the
clone boundary reads as `no-pr`; `git fetch --unshallow` removes the
boundary.

The default run id is `<until-short-sha>-<since-slug>`. A duration keeps its
digits and unit (`12w`); a hex commit id becomes its first seven characters; a
branch or tag name is lowercased with every non-alphanumeric run collapsed to
`-`. `--run <slug>` overrides it and must already be slug-shaped. An existing
`run.json` for the same id with another mode or window refuses `run-mismatch`;
delete that directory or pick another `--run`.

### Artifacts

`.verdict/backtest/<run>/run.json` holds the resolution (`spec`, `sha`, `date`,
and the `cutoff` a duration computed), the mode, the git version, `created_at`,
and the artifact paths. It is the only file carrying a timestamp or a machine
fact. `.verdict/backtest/<run>/mine.json` holds `schema_version`, `method`,
`candidates[]`, and `skipped[]`, in sorted order with no timestamp, so mining
the same window twice is byte-identical. Both are written atomically.

### Seeds and SZZ

Seeds are commits inside the window, newest first:

- subject matching `^(?i)(fix|bugfix|hotfix)(\(|:|!)`;
- a body line matching `(?i)\b(fixes|closes|resolves) #[0-9]+`;
- subject `Revert "..."`, except a revert of a revert.

A merge commit is not a seed: it is counted `merge-seed` when it matches a seed
rule. For each other seed `F`, `git show -U0 --no-ext-diff --no-textconv
--no-renames F` gives the deleted old-side lines. Each deleted line is blamed at
`F^` with `git blame --porcelain --no-ignore-revs-file -L <line>,<line>`; the
blamed commit is the introducer `I`. A `-L` outside the file's history or a
zero boundary id reads `unaligned`, never a guess.

The PR unit for `I` is the first matching rule:

- `I` is a merge: head `I^2`, base `merge-base(I^1, I^2)`, `merge-parent`.
  Conflict-resolution text usually exists at neither parent head, so the
  anchor step normally skips it as `unaligned`.
- `I`'s subject ends `(#N)`: head `I`, base `I^1`, `squash-commit`.
- otherwise the first first-parent merge in `I..until` that brought `I` into
  the first-parent line: head `M^2`, base `merge-base(M^1, M^2)`,
  `first-parent-merge`.

Every candidate records `head_source: "merge-time"`, `merged_at` (the landing
commit's committer date), and `head_at` (the head's). No pre-merge
reconstruction is invented.

### Anchor

At the PR head, `git grep -n -F -e <deleted line>` in the same path must find
the exact line text. Paths are matched literally — a `:`-leading name or a
glob-shaped name is the file name git stored, never pathspec magic. One hit is
the bug line. Several hits are accepted only when every hit blames back to
`I`; otherwise the fix is `unaligned`. A PR is one candidate, and its bugs are
the de-duplicated union (path, line) over every fix that anchors to it.

### Exclusions

Exclusions never refuse the run. Every deleted line is either a bug line in
`candidates[].bugs` or one row in `skipped[]`: a row carries the line's own
`reason`, its `fix`, the `pr` when the line resolved to one, and its position
as `detail: "path:line"` in the fix's parent. A reason is per line, so one fix
can produce several rows with different reasons. A whole-fix exclusion
(`binary`, `wide-fix`, `docs-test-only`, `added-only`) and a merge seed are
one row without a detail.

| Reason                                  | Rule                                                                                                                                                                                                                    |
| --------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `merge-seed`                            | the seed commit is a merge                                                                                                                                                                                              |
| `vendor`, `node_modules`, `third_party` | the deleted path has that component                                                                                                                                                                                     |
| `generated`                             | `*.pb.go`, `zz_generated.*`, `*.gen.go`, or a dependency lockfile (`go.sum`, `package-lock.json`, `pnpm-lock.yaml`, `yarn.lock`, `bun.lock`, `bun.lockb`, `Cargo.lock`, `composer.lock`, `Gemfile.lock`, `poetry.lock`) |
| `binary`                                | the fix's numstat reports a binary file                                                                                                                                                                                 |
| `docs-test-only`                        | every touched path is documentation or a test (`docs/`, `test/`, `tests/`, `testdata/`, `__tests__/`, `*_test.go`, `*.test.*`, `*.spec.*`, `README*`, `CHANGELOG*`, `LICENSE*`, `*.md`, `*.mdx`, `*.rst`)               |
| `wide-fix`                              | more than 200 changed lines or more than 5 files                                                                                                                                                                        |
| `added-only`                            | the fix deletes no old-side line                                                                                                                                                                                        |
| `blank-line`                            | the deleted line is whitespace                                                                                                                                                                                          |
| `no-pr`                                 | the introducer has no merge or squash PR                                                                                                                                                                                |
| `unaligned`                             | the deleted text is absent, ambiguous, or outside history at the head                                                                                                                                                   |
| `ancestor-of-since`                     | the PR landed at or before `--since`                                                                                                                                                                                    |
| `large-pr-diff`                         | the PR diff is over fetch's 512,000-byte cap                                                                                                                                                                            |

### Kinds

A bug's `kind` is a deterministic guess: the fix subject first, then the fix
diff; first match wins. Subject words match case-insensitively at a word start,
in this order:

| Kind                  | Subject words                                                                         |
| --------------------- | ------------------------------------------------------------------------------------- |
| `auth-bypass`         | `auth`, `authoriz`, `authentic`, `permission`, `privilege`, `bypass`, `admin`, `role` |
| `path-traversal`      | `traversal`, `zip slip`, `path escape`, `directory traversal`, `confine`, `path join` |
| `injection`           | `inject`, `sql`, `xss`, `command execution`, `sanitiz`                                |
| `race`                | `race`, `concurrent`, `goroutine`, `mutex`, `deadlock`, `lock`                        |
| `nil-deref`           | `nil`, `null pointer`, `nil pointer`, `dereference`                                   |
| `off-by-one`          | `off-by-one`, `off by one`, `loop bound`, `fencepost`, `boundary`                     |
| `index-out-of-range`  | `index out of range`, `index-out-of-range`, `out of range`, `panic`                   |
| `missing-error-check` | `error`, `err`, `unchecked`, `ignored`, `handle failure`                              |

When no subject word matches, the same order applies to the fix diff: an added
line carries `err != nil` or `err == nil` (`missing-error-check`); a deleted
line carries the word `nil` or `null` (`nil-deref`); a deleted line carries
`<=` and `len(` while an added line carries `<` and `len(` (`off-by-one`); an
added line carries `>= len(` or `<= 0` (`index-out-of-range`); an added line
carries `sync.Mutex`, `sync.RWMutex`, `sync/atomic`, or `atomic.` (`race`); an
added line carries `filepath.Clean` or `filepath.Rel` (`path-traversal`); an
added line carries `html.EscapeString`, `strconv.Quote`, or `?` with `SELECT`,
`INSERT`, or `UPDATE` (`injection`); an added line starts the word `authorized`,
`permission`, or `admin` (`auth-bypass`). Everything else is `unclassified`.
The first fix in newest-first order owns a duplicated path/line.

### `--json`

`data` is `{run, mode, since, until, method, artifacts, mine}`. `since` and
`until` are `{spec, sha, date[, cutoff]}`; `artifacts` is `{run, mine}` with
slash paths relative to the repository root; `mine` is `{candidates, skipped}`.
A candidate carries `head`, `base`, `landed_by`, `pr` (number or null),
`reconstruction`, `head_source`, `merged_at`, `head_at`, `fixes[]`, and
`bugs[]`; a bug carries `path`, `line`, `kind`, and its `fix`. The envelope's
`repo` is empty and `pr` is null: mining targets the checkout, not a bundle.
Human output is one header line, one line per candidate, a skipped-by-reason
footer, and the `not citable — SZZ-derived` line.

## Out of scope by design

Hosted settings, metered proxies, token mints, server-seeded comments and checks,
approval gating, auto-merge, cross-run persistence, upload signing, credential pools,
watch feeds, signed-commit flows. For on-demand local review none of these move
quality — the prompts do.

## Published schemas + drift rule

The shapes agents write and operators parse are pinned in `schemas/`
(draft 2020-12, shipped in the npm tarball): `review.schema.json`
(agent-written input), `findings.schema.json` (the merged findings store),
`cost.schema.json` (the self-reported spend record), `controls.schema.json`
(the resolved base-owned controls snapshot), `usage.schema.json` (the
explicit usage handoff), `corpus.schema.json` (benchmark case manifests),
`post.schema.json` and `record.schema.json` (completed post/record captures),
`threads.schema.json` (the normalized PR conversation),
`pack.schema.json` (the lens pack manifest),
and `envelope.schema.json` (the `--json` envelope plus the stable per-verb
`data` for `report`, `post`, `record`, and `show`, plus fetch's additive
estimate). `schemas/validate.sh` is the
offline checker: a shim over `verdict __validate-schema`, whose implementation
lives in `internal/schemacheck` — the one keyword subset the published schemas
use (`$ref` within a file or across sibling files, `type`, `const`, `enum`,
`properties`, `required`, `items`, `additionalProperties` as a bool or a
schema, `minimum`/`maximum`, `minLength`/`maxLength`, `pattern`, `anyOf`,
`format: date-time`), with anything outside it failing loudly and an
unresolvable `$ref` never validating. The Go conformance tests and the evals
therefore run the same code, so a fixture cannot pass one side and fail the
other; `evals/cases/schema-contract.sh` asserts representative fixtures
validate — a shape change that breaks agents or parsers fails the build
instead of a review.

Drift rule: additive within `schema_version`/`sig_version` `"1"` — new
optional keys pass through the stores and the schemas alike; renames,
tighter types, and newly required keys never happen without a version bump,
and a bump ships only by operator decision.
