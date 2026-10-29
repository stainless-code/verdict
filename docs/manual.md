# Verdict — operator manual

Run PR reviews **locally, on-demand, with your own models**. The agent reviews;
`gh` does GitHub I/O. No servers, no external services.

## Quickstart

```bash
chmod +x scripts/review.sh
export GH_TOKEN="$(gh auth token)"   # or a PAT with contents:read, pull-requests:write, issues:write

# 1. fetch
./scripts/review.sh fetch owner/repo 123

# 2. review with ANY agent:
#    Read SKILL.md, prompts/system.md, prompts/review.md, .review/123/PR.json,
#    .review/123/roles.txt. Read .review/123/full.diff fully via .review/123/toc.txt.
#    Fan out 0-N lenses per prompts/reviewer-subagent.md for disposition-changing questions only.
#    Write exactly one .review/123/review.json {schema_version: 1, body, approved, comments[], resolved?, triage?}.

# 3. post (exactly once — validates anchors, merges findings, submits)
./scripts/review.sh post owner/repo 123
```

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
      "suggestion": "    fixed code…\n"
    }
  ],
  "resolved": ["a1b2c3d4"],
  "triage": [
    {
      "sig": "e5f6…",
      "status": "wont-fix",
      "rationale": "By design: … (≥30 chars)"
    }
  ]
}
```

Inline rules: `path` should be in the diff (unknown paths are kept with a warning, never refused), `line` = NEW-side line inside
an `@@` hunk (`side` RIGHT default, LEFT for deleted lines), multi-line needs `start_line` in the
same hunk, `suggestion` must preserve exact indentation. `post` checks every anchor against
the fetched hunks: provably-outside anchors move into the body under Unanchored observations
(one bad anchor never fails the run); ambiguous anchors are kept with a warning.
Suggestions render as fenced blocks in `body` (raw keys never hit the API).
`resolved` lists finding sigs (short ids from the prior `findings.json` table) the run addresses.

## Findings store + triage authority

`post` writes `.review/N/findings.json`: one record per inline comment and body `###`
section with a stable line-number-free signature, `status` (`open`/`addressed`/`wont-fix`/
`false-positive`/`deferred`), first/last seen SHAs, evidence rung (`said`/`pointed`/`walked`/
`ran`/`reproduced`, default `pointed`), rationale for terminal states, and append-only `history`.
Incremental runs dedup by sig — recorded findings are not re-reported unless the delta changes them. Before posting, `post`
prints a fail-closed preflight block (body verdict, anchor counts, finding counts, hunk index).
Binding verdicts need `--bind` (else downgraded to `COMMENT`); `APPROVE` is refused while
`open` + `deferred` findings stand (`--force` overrides).

Authority rule: **the script disposes, the agent proposes, the human vetoes.**
Bare `resolved[]` sigs record a claim but never flip status. `addressed` requires
`.review/N/verification.json` evidence (`{sig, verdict: VERIFIED, verifyCmd, resultHead}`
with `resultHead` matching HEAD — the revalidator writes it); `NOT VERIFIED` reopens,
`INCONCLUSIVE` only logs. `triage[]` claims other states with rationale (unknown enum or
unknown sig refused/ignored; short rationale keeps prior status). The operator mutates the
store directly between runs with `./scripts/review.sh triage … --sig … --status … --rationale …`
(human supremacy, every override history-logged).

### Sig contract (sig_version 1)

The sig is the cross-run join key — findings, deltas, calibration, and any external
policy/gating tool all join on exact sig equality. Derivation: `sigKey` is
`path + "\n" + heading + "\n" + body` (inline findings use `path`, section findings
use `heading`); normalize by squashing every whitespace run (spaces, tabs, newlines)
to one space and trimming ends; take the lowercase hex SHA-1. Line numbers, sides,
severity, and rigor are deliberately excluded, so findings survive line shifts and
re-worded metadata. Every entry stamps `sig_version: "1"`; merges backfill it onto
older entries produced by the same normalization. Changing the normalization orphans
history — that ships as a new `sig_version` with a migration note here, never silently.
`review.json` inputs stamp `schema_version: "1"` (`post`/`record` refuse unknown
versions, warn-and-proceed when the stamp is missing); every bundle carries
`.review/N/store.json` (`{"schema_version":"1"}`) marking the generation of the
whole store — plugins read it instead of sniffing shapes. `PR.json`/`reviews.json`/
`inline.json` are raw GitHub output (GitHub's schema, unversioned); `post.json` and
`record.json` carry `schema_version: "1"`.

### Bundle namespace (core files vs `x-*`)

A bundle dir holds exactly the core-owned files (`PR.json`/`source.json`,
`full.diff`, `toc.txt`, `roles.txt`, `review.json`, `findings.json`, `findings.dryrun.json`, `record.json`,
`post.json`, `store.json`, `hunks.tsv`, `reviews.json`, `inline.json`, `panel`,
`patch.json`, `verification.json`, `incremental.diff`, plus dot-scratch
`.difffiles`/`.posted_*`/`.compare.err`/`.diff.err`/`.post.err`). Router-authorized
agent terminal writes (`report.md` via `prompts/ci.md`, `replies.json` via
`prompts/address-reviews.md`, `noreview.md` via `prompts/incremental-review.md`)
are core-tolerated scratch: core verbs never read them. Anything else a
plugin or future verb adds MUST live under `x-*` (`x-*.json` recommended) — core
verbs never read or write `x-*`, so plugins can't collide with core files and
`check-tree` (which ignores all of `.review/`) never trips on them. Agents write
only `review.json` (plus `verification.json`/`patch.json`/`report.md`/`replies.json`/`noreview.md`
where a mode file says so);
everything else in a bundle dir is script-owned.

## Learnings (durable repo memory)

`.review/learnings.md` at the repo root is the skill's only cross-run memory: durable
knowledge (test commands, conventions, gotchas) in `##`-headed sections. `fetch` reports
it when present; agents read headings first and challenge stale claims. Post-run reflection
appends durable lessons (never transcripts). Deleting a wrong entry beats editing around it.

## Machine surface

`fetch`/`post`/`triage`/`report`/`next`/`show`/`verify`/`doctor`/`calibrate`/`fetch-local`/`record`/`adopt` take `--json`: stdout is exactly one JSON envelope
(`schema_version`, `verb`, `repo`, `pr`, `at`, `next`, `warnings`, `data`, `error`),
all human progress goes to stderr, failures emit `{data: null, error: {code, message}}`
non-zero. `report` is the pure offline reader over the store (ranked items, verdict,
counts, patch passthrough, optional `--output` markdown with machine-header markers,
optional `--sarif` for scanners/CI). Distinct from `prompts/ci.md`'s agent-written
`.review/N/report.md` (no-post CI loop, `SKILL.md` router row) — never point
`report --output` at that path; the verb's markdown carries `report-machine`
markers, the agent's does not. Severity is stored at write time (unknown → null,
prose emoji never parsed) — rank uses status, then severity, then rigor, then
anchored/inBounds, then path/line/sig. `doctor` preflights the harness; `calibrate`
joins findings against live ground truth, read-only. Sigs follow the Sig contract under Findings store.

## Model wiring

This kit uses whatever agent you invoke — your current model is the review model.
Lenses can run cheaper: map each lens to a smaller sibling of the main model
(e.g. flagship → sonnet-class) via the `lens:<label>` dispatch template, at most
~12 files and 10 findings per lens. Formation is roles, never slugs: strongest reasoner
synthesizes, 2+ diverse cheaper models lens, different families when possible.

For arena-style parallel runs, pin the panel outside the skill: `REVIEW_PANEL` env
(comma/space separated) or `.review/panel` (one slug per line, `#` comments).
`fetch` resolves it to `.review/N/panel` — first entry synthesizes, the rest staff lenses,
used exactly in order, never substituted. Same panel every run keeps verdicts comparable
and costs capped; no panel means the operator picks per run. Slugs live in your config
because they rot; the skill stays name-free.

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

Flags are mutually exclusive (one diff source per bundle); empty diff exits 0 with
"nothing to review". Writes `.review/local-<name>/` (`source.json`, `full.diff`,
`toc.txt`, `roles.txt`) — `source.json` stands in for `PR.json` (kind, base/head
refs+SHAs, range, dirty flag, diff sha256, tool version). The `local-` namespace can
never collide with a numeric PR dir; downstream verbs address the bundle as
`local-<name>`.

`record owner/repo local-<name>` runs the validate+merge half of `post` with no
POST: hunk index, keep-biased anchor partition, sig merge with append-only history,
resolved/verification/triage claims, writes `findings.json` + `record.json`.
`report`/`triage`/`next`/`show`/`verify` work unchanged on local bundles (`report`
reads `head_sha` from `source.json` when `PR.json` is absent). `post` refuses local
bundles (no PR to post to); `calibrate` refuses them too (no threads to settle) —
record predictions locally, settle them after the upgrade below.

Upgrade arc: review locally, then `fetch owner/repo N` and
`adopt owner/repo N --from local-<name>`. Adopt copies findings with an `adopted`
history event and re-resolves anchors against the PR hunks (unmatched paths land
`anchored:false`, never dropped), refuses into a non-empty store. From there `post`
and `calibrate` proceed normally — predictions recorded offline get settled against
live ground truth.

## Providers (non-GitHub hosts)

`fetch --provider gitlab|gitcafe|forgejo|bitbucket|azure` builds the same bundle
shape from another host (`PR.json` normalized to one schema: `provider`,
`number/title/body`, `base_ref/head_ref`, `base_sha/head_sha`, `author`, `url`,
`state` as open|closed|merged, `draft`, per-file `additions/deletions`).
`write_toc_roles` and the whole validate+merge path run unchanged — anchors
validate against `full.diff` hunks regardless of host. `record`, `report`,
`triage`, `show`, `next` work on provider bundles as-is. `post`, `calibrate`,
and `adopt`-into refuse non-GitHub bundles: none of these hosts has GitHub's
single atomic review submit (GitLab needs draft-notes-then-publish, Bitbucket
and Azure need N thread POSTs, Forgejo's CLIs can't author inline comments),
so submit stays a per-host follow-up rather than unverified code.

| Provider    | Fetch reads                                                                                               | Auth                                                             | Gaps (v1)                                                               |
| ----------- | --------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------- | ----------------------------------------------------------------------- |
| `gitlab`    | `glab mr view/diff/note list` + changes API                                                               | `glab auth login` (or `GITLAB_TOKEN`)                            | experimental note commands; no incremental (`--base-sha` refused)       |
| `gitcafe`   | `cafe pr view/diff/reviews/comments`                                                                      | `cafe auth login` (or `CAFE_TOKEN`)                              | CLI has no range anchors; no suggestion blocks; no PR URL (stored null) |
| `forgejo`   | REST `curl` (`$FORGEJO_HOST/api/v1`, `$FORGEJO_TOKEN`) — neither `fj` nor `tea` exposes JSON review reads | token env                                                        | inline threads aggregated per review id                                 |
| `bitbucket` | REST `curl` (`$BITBUCKET_TOKEN` Bearer or user+password Basic)                                            | token env                                                        | diffstat/comments paginated server-side; no grouped submit              |
| `azure`     | `az repos pr show` + `az rest` threads/iterations; diff via temp-clone `git fetch` of the two SHAs        | `az login` / `AZURE_DEVOPS_EXT_PAT`, `AZURE_DEVOPS_ORG` required | needs git reachability to the repo remote; no org/project auto-detect   |

State maps to open|closed|merged at fetch (`opened`→open, `active`→open,
`ABANDONED`→closed, `SUPERSEDED`→closed, `completed`/`merged`→merged;
anything else lands `unknown`, which `post` would refuse — but post is
refused earlier anyway). File +/- stats come from the host API where it
provides them (GitCafe, Forgejo, Bitbucket); otherwise they are derived
from the reviewed diff itself (`git apply --numstat`), so the coverage
manifest always describes the fetched object.

## Calibration (predictions vs ground truth)

Everything the store records is a prediction; `calibrate <owner/repo> <pr>` checks it.
Read-only join (threads + one compare + PR state), keep-biased:

- Sources: live review threads (GraphQL, matched by the exact body we sent — robust, no
  fuzzy matching) → per-finding outcome; `post.json:review.id` records which review posted them;
  one `compare` of review head vs live head for changed-file evidence; PR merged state as context. Terminal findings are
  skipped, not judged.
- Outcomes: `fixed` (cited file changed + thread resolved), `resolved-unfixed` (resolved
  without touching the cited file — disagreement or fix-elsewhere), `open` (still open),
  `unknown` (comment not found, review deleted, or ambiguous — never judged).
- Rules: resolved ≠ fixed; `unknown` never counts against precision; merged-with-open is
  recorded, not judged; tier precision reported separately — a wrong `critical` costs more
  than a wrong `informational`.
- Use: per-model verdict calibration over time (which model judges well feeds the panel file).
  Never auto-tunes thresholds — humans read the report, the script only joins.
- Enabler: `post` records `.review/N/post.json` (`review` id, `head`, `posted_at`)
  on every real post. `calibrate` refuses without it.

## Out of scope by design

Hosted settings, metered proxies, token mints, server-seeded comments and checks,
approval gating, auto-merge, cross-run persistence, upload signing, credential pools,
watch feeds, signed-commit flows. For on-demand local review none of these move
quality — the prompts do.

## Store changelog (additive within schema_version "1" — renames never happen, only additions)

Schema evolution only — verbs and features ship in the release changelog.

- Initial: findings records `{sig, kind, path/heading, line, side, anchored, body}` + `status: open/addressed` + `firstSeen/lastSeen` + append-only `history`.
- Added: `rigor`, `inBounds`, retained `suggestion`, `rationale`/`triagedAt`/`triagedBy`, history `actor`/`sha`; statuses `wont-fix/false-positive/deferred`; `verification.json` evidence gate; `patch.json` gains `verifyCmd/verifyOutput/baseHead/resultHead`.
- Added: `post.json` capture `{review, head, posted_at}` (calibration enabler).
- Added: local bundles — `source.json` standing in for `PR.json` under `.review/local-<name>/`.
- Added: store versioning — per-bundle `store.json` marker, `sig_version: "1"` on
  findings entries (backfilled on merge), `schema_version: "1"` on `review.json`
  inputs (unknown refused, missing warned), `post.json`, `record.json`,
  `source.json`; sig derivation specced as the cross-run join key; `x-*` plugin
  artifact namespace reserved in bundle dirs.
