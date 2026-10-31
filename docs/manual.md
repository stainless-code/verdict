# Verdict — operator manual

Run PR reviews **locally, on-demand, with your own models**. The agent reviews;
`gh` does GitHub I/O. No servers, no external services.

## Quickstart

```bash
chmod +x scripts/review.sh
export GH_TOKEN="$(gh auth token)"   # or a PAT with contents:read, pull-requests:write, issues:write

# 0. onboard once per repo (optional, offline): scaffold .review/ controls
./scripts/review.sh init --preset standard --yes

# 1. fetch
./scripts/review.sh fetch owner/repo 123

# 2. review with ANY agent:
#    Read SKILL.md, prompts/system.md, prompts/review.md, .review/123/PR.json,
#    .review/123/roles.txt. Read .review/123/full.diff fully via .review/123/toc.txt.
#    Fan out 0-N lenses per prompts/reviewer-subagent.md for disposition-changing questions only.
#    Write exactly one .review/123/review.json {schema_version: 1, body, approved, comments[], resolved?, triage?, sections?}.

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
  ],
  "sections": [
    {
      "heading": "### ⚠️ Rollout needs a migration window",
      "severity": "important"
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
`sections` carries severity tags `{heading, severity}` for `###` body sections — exact heading string, unmatched tags ignored.

The commentable set is pinned at fetch time: `fetch`/`fetch-local` writes
`.review/<bundle>/hunks.tsv` (per-file, side-aware `NEW`/`OLD` ranges from
the fetched diff) and `post`/`record` validate anchors against that pinned
snapshot. A push that lands after fetch only adds drift context (the
stale-`HEAD` note) — it never moves the validation target. Anchors outside
the snapshot move into the body under `### ℹ️ Unanchored observations`,
each named with its path/line and reason (e.g. `anchor 50-50 (RIGHT)
outside reviewed hunks`); drops never fail the run. If the snapshot is
missing (pre-snapshot bundles), the run rebuilds it from `full.diff` with a
best-effort warning — re-fetch to pin it.

## Findings store + triage authority

`post` writes `.review/N/findings.json`: one record per inline comment and body `###`
section with a stable line-number-free signature, `status` (`open`/`addressed`/`wont-fix`/
`false-positive`/`deferred`), first/last seen SHAs, evidence rung (`said`/`pointed`/`walked`/
`ran`/`reproduced`, default `pointed`), rationale for terminal states, and append-only `history`.
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
`patch.json`, `verification.json`, `incremental.diff`, `kb.txt`, plus dot-scratch
`.difffiles`/`.posted_*`/`.compare.err`/`.diff.err`/`.post.err`/`.recheck.err`). Router-authorized
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

## Knowledge base (scoped local rules)

`.review/kb.jsonl` at the repo root is the structured layer above `learnings.md`:
one JSON object per line — `{id, rule, scope, provenance, rigor, hits,
false_hits, judged, status}` (`id` matches `^[a-z0-9][a-z0-9-]{0,64}$`,
`scope` is a non-empty comma-separated glob list, `rigor` is
`said`/`pointed`/`walked`/`ran`/`reproduced`, counters are non-negative ints
with `judged == hits+false_hits`, `status` is `active`/`retired`). Commit it;
it compounds.

`learn` manages it (no repo/PR args, no network):

```bash
./scripts/review.sh learn --rule "Check error paths on every await" --scope "src/*.ts" --provenance manual --rigor walked
./scripts/review.sh learn --status            # rules + counters + precision
./scripts/review.sh learn --retire <id> --reason "<30+ chars, on the record>"
./scripts/review.sh learn --restore <id> --reason "<30+ chars, on the record>"
```

`--id` is optional (slugged from the rule text when absent). Exact-duplicate
rule text and taken ids are refused; empty rule/scope and bad rigor/id fail
typed. Retire keeps the tombstone line (`status`, `retiredAt`,
`retireReason`, history-logged) with counters untouched; restore flips back to
`active` keeping the tombstone as record.

Recall: `fetch`/`fetch-local` writes `.review/<bundle>/kb.txt` when active
rules scope-match the diff — top 3 by precision (`judged==0` reads 0.5),
tiebreak judged desc then id. Agents read it as context, never orders
(contract line in `prompts/review.md`); clean diffs leave no file, and with no
`kb.jsonl` nothing changes at all.

The recall manifest `.review/<bundle>/kb-recall.json` mirrors the note
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
`.review/kb-joins.jsonl` audit row carrying `bundle` (the bundle dir name)
and `review_id` (the posted review id, null when no post.json exists yet —
local bundles and unposted PRs).

Retirement is data-driven: after any credit, a rule with `judged` at/above
`REVIEW_KB_MIN_JUDGED` (default 5) and `false_hits/judged` above
`REVIEW_KB_MAX_FP_RATE` (default 0.5) retires with a tombstone. Env wins, then
`.review/kb-policy` (`MIN_JUDGED=`/`MAX_FP_RATE=` lines), then defaults;
invalid values warn and fall back, never failing the flip. Retired rules never
recall; citing one warns and drops.

## Machine surface

`fetch`/`post`/`triage`/`report`/`next`/`show`/`verify`/`doctor`/`calibrate`/`fetch-local`/`record`/`adopt`/`learn`/`init`/`install` take `--json`: stdout is exactly one JSON envelope
(`schema_version`, `verb`, `repo`, `pr`, `at`, `next`, `warnings`, `data`, `error`),
all human progress goes to stderr, failures emit `{data: null, error: {code, message}}`
non-zero. `report` is the pure offline reader over the store (ranked items, verdict,
counts, patch passthrough, optional `--output` markdown with machine-header markers,
optional `--sarif` for scanners/CI). `--output` markdown leads with
`> Confidence: … · Safety: …` (from the stored `post.json`/`record.json`;
`unrecorded`/`not assessed` when the run recorded neither) and, on clean
reports (no open findings), closes with a `## What we checked` list of at
most seven bundle files (plus an overflow pointer to `toc.txt`). Under the
lead, `--output` renders a `> Basis: …` line computed exactly from the
findings store (rigor rung counts in evidence order, distinct cited kb rule
ids, addressed findings as verified fixes — covering the whole store
regardless of display filters; the agent-authored `> Basis:` line in `review.json` validates against the review's own comments instead, so carried-over findings never warn). Every `--output` file and every posted body
ends with one merged footer block (tier, cost or unrecorded, files
reviewed, review link when known, plus a leveled attribution line with
logo) — see
Publish footer + attribution knobs. Item bodies
stay 200-char summaries; full reasoning lives in the `--json` envelope only.
Distinct from `prompts/ci.md`'s agent-written
`.review/N/report.md` (no-post CI loop, `SKILL.md` router row) — never point
`report --output` at that path; the verb's markdown carries `report-machine`
markers, the agent's does not. Severity is stored at write time (unknown → null,
prose emoji never parsed) — rank uses status, then severity, then rigor, then
anchored/inBounds, then path/line/sig. `doctor` preflights the harness; `calibrate`
joins findings against live ground truth, read-only.

`fetch`/`fetch-local` take `--show-prompts`: after the normal bundle write,
the script renders the assembled agent reading list — `SKILL.md`,
`prompts/system.md`, `prompts/review.md`, `prompts/diagrams.md`, the bundle
meta (`PR.json` or `source.json`), `toc.txt`, `roles.txt`, `full.diff`, plus
`kb.txt`, `sensitive.txt`, and `panel` when present, concatenated in that
order — to stdout (human mode; progress moves to stderr) or
`data.prompt_dump` (`--json`). No agent runs, no network beyond the fetch
itself, no store writes beyond the bundle: without the flag both verbs are
byte-identical.

`ready` stays advisory by default (exit 0, blockers listed); `ready --check`
opts into gating for hooks/CI with machine exits — 0 ready, 1 blockers-or-wait,
2 usage, 3 tool error — so callers branch without parsing JSON:

```bash
./scripts/review.sh ready owner/repo 123 --check
case $? in
  0) ./scripts/review.sh fetch owner/repo 123;;  # reviewable — review
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
./scripts/review.sh report owner/repo 123 --check important
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

`report --completeness` answers a second question from the same read — "is
HEAD reviewed?" — by comparing the bundle's fetched HEAD against the local
completed-post record (`post.json`, written only by a real `post`):
a match passes; a missing, unparsable, head-less, or mismatched record
fails with a typed `missing-post`/`corrupt-post`/`stale-head` check line
naming the re-fetch command. Alone it gates on HEAD-reviewed only;
combined (`--check critical --completeness`) it fails when tier-hits OR
stale/missing post do. Pure local reads, no network; without the flag
output and exits are exactly today's.

Hook recipe (merge gate — findings AND freshness in one call):

```bash
./scripts/review.sh report owner/repo 123 --check critical --completeness || exit 1
# exit 0 only when no open critical+ findings stand AND the fetched HEAD
# is the posted HEAD; exit 1 names which dimension failed on stderr.
```

Sigs follow the Sig contract under Findings store.

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

### Cost control (budget, estimate, consent)

Cost accounting (`report --cost`, per-run `cost.json` with effort tiers) grows teeth
only when the operator sets a spend budget: `REVIEW_BUDGET_USD` env (wins) or a
`.review/budget` file holding one plain USD number (`#` comments and blanks skipped).
Unset disables the whole plane — no estimate line, no gate, no receipts.

With a budget set, `fetch`/`fetch-local` print a pre-run `» estimate` line before any
network: the most-run effort tier across comparable past runs (every
`.review/*/cost.json` record, same tier rules as the rollup; ties break toward the
deeper tier) and its mean `est_usd`. No history, or no priced run in the projected
tier, reads as `unknown` — never invented, never blocking. A numeric projection over
budget refuses with typed `budget-exceeded` unless the run carries
`--consent-over-budget`; consented runs append one receipt to the bundle's
`budget.json`, which `report --cost` surfaces as a `receipts:` line (and a `receipts`
key in the JSON cost object). Threshold is the budget itself: projected above budget
needs consent, at-or-under proceeds. Estimates cover the review run about to start,
not the fetch reads; unknown estimates never refuse.

## Onboarding (scaffolder + prefs file + skill installer)

New operators onboard in one offline command — no network, ever:

```bash
./scripts/review.sh init --preset standard --yes   # chill|standard|strict, default standard
```

`init` writes the standard local control files, then runs check-tree
validation (same filter: own `.review/` scratch ignored). Existing files
print a preview and need `--yes`/`--force` (or an interactive confirm when
stdin is a tty) before overwrite — the same preview-then-confirm idiom the
installer uses. Re-running with the same preset reads as `keep` (idempotent).

Style presets map to panel strictness plus KB retirement thresholds (the KB
starter is identical across presets):

| preset     | `.review/budget` | `.review/panel` members    | `.review/kb-policy`                                      |
| ---------- | ---------------- | -------------------------- | -------------------------------------------------------- |
| `chill`    | `2.00` (lenient) | none (comments only)       | `MIN_JUDGED=10` `MAX_FP_RATE=0.7` (retires rarely)       |
| `standard` | `0.50` (default) | `main` (one synthesizer)   | `MIN_JUDGED=5` `MAX_FP_RATE=0.5` (matches code defaults) |
| `strict`   | `0.10` (tight)   | `lead`, `lens-1`, `lens-2` | `MIN_JUDGED=3` `MAX_FP_RATE=0.3` (retires aggressively)  |

`.review/kb.jsonl` starts with two starter rules (`check-error-paths`,
`verify-before-claim`, both `status: active`, counters zeroed) ready for
`learn --status`. The `verify-before-claim` starter uses scope `*`, so it
recalls on every bundle until you narrow it — tune scopes early, then replace
the panel placeholders with real slugs — same
panel every run keeps verdicts comparable and costs capped.

The prefs file ends env sprawl without forcing migration. Path:
`${XDG_CONFIG_HOME:-$HOME/.config}/verdict/settings.json` (`$XDG_CONFIG_HOME`
respected, `$schema` key carries a schema hint that is never fetched).
Presentation keys only — CLI survey: the only presentation surfaces are the
machine envelope (`--json`), file outputs (`--output`/`--sarif`), and the
prompt-dump destination, so the file exposes exactly three namespaces:

```json
{
  "$schema": "https://github.com/stainless-code/verdict/blob/main/schemas/settings.schema.json",
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
  (warnings/errors); the tool emits no ANSI today, so `auto`/`never` read
  plain and only `always` adds codes. Machine envelopes never carry codes.

Precedence is flags > file > env (`REVIEW_OUTPUT_FORMAT`,
`REVIEW_LAYOUT_COMPACT`, `REVIEW_COLOR_MODE`, plus `REVIEW_PUBLISH_FOOTER`,
`REVIEW_PUBLISH_ATTRIBUTION` (`off`|`product`|`full`), `REVIEW_PUBLISH_ATTRIBUTION_TEXT`,
`REVIEW_PUBLISH_COPYRIGHT`, `REVIEW_PUBLISH_LOGO`, `REVIEW_PUBLISH_LOGO_URL`
for the footer below) > defaults; global flags
`--color <mode>`, `--compact`/`--no-compact`, `--no-json` work on any verb
(`--json` itself stays per-verb and already wins over a text default).
`NO_COLOR` applies only when color is otherwise unset. Any other top-level
key (including server-shaped or `org.*` namespaces) refuses with typed
`bad-settings`; any credential-shaped key at any depth (`token`/`key`/`auth`/
`secret`/`password`/`pat`/`credential`/`bearer` and compounds) refuses with
typed `bad-settings-credentials` — credentials never live in the file.
Absent file (or a file holding only defaults) keeps every gate
byte-identical to today (marker-first, degenerate-body bar unchanged); only
the rendered bytes gain the default footer block below — no forced
migration, no env removed.

## Publish footer + attribution knobs

Every posted body and every `report --output` file ends with one merged
block at the end:

```markdown
---

<!-- verdict-footer -->

> <a href="https://stainless-code.com/verdict"><img src="https://raw.githubusercontent.com/stainless-code/verdict/main/apps/docs/public/logo.svg" alt="Verdict" width="18" height="18"></a> Reviewed with [**Verdict 0.2.0**](https://stainless-code.com/verdict) · standard tier · cost $0.05 · 4 files · [review `5722667325`](https://github.com/acme/app/pull/9#pullrequestreview-5722667325)
```

Fields degrade factually, never blank, never failing the run: an unreadable
version reads `unknown`, unpriced spend reads `cost unrecorded`, uncountable
files read `files unrecorded`, and the review link renders only when a
completed post record carries it (never invented pre-post). The attribution
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

Migration note (P1b breaks the day-old P1 default): `publish.attribution`
was a bool in 0.2.0 and is now `off`/`product`/`full` — a file carrying
`true`/`false` refuses with the mapping named (`"off"` for `false`,
`"product"` for `true`). `--attribution`/`--no-attribution` keep working
(product/off); `--attribution full` reaches the maker level.

Confidence is the agent's "would I merge on this review" judgment, informed by evidence depth, coverage, and rule support.
The scale stays 1-5 across
runs so past verdicts stay comparable (no rescaling; a computed score stays
deferred until machine-verifiable inputs exist, with kb-measure X2/X3
outputs as the named trigger). `report --output` renders the store-wide Basis
line; the agent's posted line covers its own review — on a re-review with carried-over
findings the two legitimately differ, and validation compares the posted line
against the review's own comments.

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
the P3 topology half above), `publish.diagrams.expanded` (bool, default
`false` — renders the kept block open instead of collapsed). Flags
`--diagram-behavioral` / `--no-diagram-behavioral` (plus
the matching `-structural` / `-expanded` pairs) win over the file; the file
wins over `REVIEW_PUBLISH_DIAGRAMS_BEHAVIORAL` / `_STRUCTURAL` / `_EXPANDED`
env; env wins over defaults. With no diagram in the body the script path is
byte-identical whether the knobs are set or not.

## Section overrides (per-block template fragments)

House style lives in four operator-owned fragments under `.review/overrides/`
(bundle-adjacent and repo-local like `.review/panel`, `.review/budget` and
`.review/kb.jsonl` — committable per-repo and check-tree-ignored; not the skill
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
`.review/overrides/` dir the script path is byte-identical to today.

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

### Fix loops (bounded, verify-gated)

`fix owner/repo local-<name> [--sig <s>] [--json]` runs one bounded pass over a
local bundle: it picks the top open inline sig (severity `critical` above
`important` above `informational`, then path) or the `--sig` prefix (must be open
and inline; non-open, section, and unknown sigs refuse), prints the finding plus
the full `kb.txt` context when present with patch → verify → land → re-run instructions, and records
the pass in `.review/local-<name>/fix.json`. The agent patches the anchor file,
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
`REVIEW_BUDGET_USD` (or `.review/budget`) before looping a large backlog. Ceilings
constrain a cooperating driver, not an adversary (deleting `fix.json` restarts
counts) — the ledger is an audit trail, not a lock.

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

| Provider    | Fetch reads                                                                                               | Auth                                                             | Gaps (v1)                                                                                          |
| ----------- | --------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------- | -------------------------------------------------------------------------------------------------- |
| `gitlab`    | `glab mr view/diff/note list` + changes API                                                               | `glab auth login` (or `GITLAB_TOKEN`)                            | experimental note commands; no incremental (`--base-sha` refused)                                  |
| `gitcafe`   | `cafe pr view/diff/reviews/comments`                                                                      | `cafe auth login` (or `CAFE_TOKEN`)                              | reads `.data.*` envelope; threads paged via `--cursor`; hunks render only when `.patch` text ships |
| `forgejo`   | REST `curl` (`$FORGEJO_HOST/api/v1`, `$FORGEJO_TOKEN`) — neither `fj` nor `tea` exposes JSON review reads | token env                                                        | inline threads aggregated per review id                                                            |
| `bitbucket` | REST `curl` (`$BITBUCKET_TOKEN` Bearer or user+password Basic)                                            | token env                                                        | diffstat/comments paginated server-side; no grouped submit                                         |
| `azure`     | `az repos pr show` + `az rest` threads/iterations; diff via temp-clone `git fetch` of the two SHAs        | `az login` / `AZURE_DEVOPS_EXT_PAT`, `AZURE_DEVOPS_ORG` required | needs git reachability to the repo remote; no org/project auto-detect                              |

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

The offline counterpart is the self-grading probe (X3): `calibrate
<owner/repo> <pr> --probe` writes one row per finding to
`.review/<bundle>/kb-grades.jsonl`, each row carrying two separable parts. The
script-owned baseline grades every finding against its triage disposition
(`addressed` = real, `false-positive` = false, anything else = `unknown`,
never forced) with the first cited rule and its helpfulness (`true` on real,
`false` on false alarms, `null` otherwise) — regenerated from `findings.json`
on every run. The agent judgment loop records sampled re-reads of closed
findings against the code in `agent_verdict` / `agent_helpful` columns that
MAY diverge from the baseline, merged machine-readably via `--judge
SIG:VERDICT[:HELPFUL]` or `--judge-file PATH` (every sig validated against
the findings store; unknown sigs refuse typed), with `agreement` computed per
row. The M4-consumable number is the agreement rate over doubly-judged rows
with the sample size recorded alongside. The run is additive-only (that one
file; findings, joins, and calibration outputs stay byte-identical),
deterministic and idempotent, and needs no post record and no network — local
bundles grade too. The agent side lives in `prompts/grade.md`: sample closed
findings, re-read the code, write judgments — advisory prose supplements the
record, it is not the record.

## Trend (portfolio outcomes over time)

`trend <owner/repo>` rolls up every reviewed bundle (`.review/*/findings.json`)
into one time series: findings opened/addressed/closed per review date, broken
down by severity tier and status, plus per-bundle rows (review time, models and
effort from `cost.json` when the agent wrote one). Pure read — no network,
nothing written, recomputed every run so hand-edited stores stay truthful;
missing or unreadable stores warn and skip, and no reviewed bundles reads as
zeros, never an error. `closed` means no longer actionable
(`wont-fix`/`false-positive`/`deferred`); null severity reads `untagged`,
mirroring `report --check`; counts are per store, so adopted findings appear in
both the local and the PR bundle. Feed for the calibration narrative: per-bundle
models plus per-tier outcomes is the raw material for per-model tier precision
over time — run `calibrate` on recent bundles to settle what the trend only counts.

`trend --overlap` adds the KB overlap audit to the same rollup: an offline join
of `kb-recall.json` manifests, findings `kb` cites, and `.review/kb-joins.jsonl`
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
- Added: review-level `confidence` (1–5) + `security_summary` on `record.json`/`post.json`
  (optional, absent → null; out-of-range confidence warns and drops, never fails).
- Added: local bundles — `source.json` standing in for `PR.json` under `.review/local-<name>/`.
- Added: store versioning — per-bundle `store.json` marker, `sig_version: "1"` on
  findings entries (backfilled on merge), `schema_version: "1"` on `review.json`
  inputs (unknown refused, missing warned), `post.json`, `record.json`,
  `source.json`; sig derivation specced as the cross-run join key; `x-*` plugin
  artifact namespace reserved in bundle dirs.
- Added: findings entries carry `kb` (cited knowledge-base rule ids, `[]` when
  uncited; excluded from sig derivation like rigor/severity); bundles may carry
  `kb.txt` (top-3 recalled rules as context); repo root gains `.review/kb.jsonl`
  (rule store), `.review/kb-joins.jsonl` (credit audit), `.review/kb-policy`
  (retirement thresholds).
