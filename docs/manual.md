# Local PR review kit — manual

Run PR reviews **locally, on-demand, with your own models**. The agent reviews;
`gh` does GitHub I/O. No servers, no external services.

## Contents

```
verdict/
  SKILL.md                    # skill entry point (feed to any agent)
  prompts/system.md           # orchestrator persona + rules + scope gate
  prompts/review.md           # full review mode + body format + severity ladder
  prompts/incremental-review.md  # delta re-review after new commits (sig dedup)
  prompts/reviewer-subagent.md   # read-only lens subagent contract + dispatch template
  prompts/address-reviews.md  # reply to / fix review threads (revalidator-gated)
  prompts/fix-ci.md           # repair PR-introduced CI failures
  prompts/build.md            # implement + self-review + open PR
  prompts/ci.md               # read-only pipeline report, never posts
  scripts/review.sh           # fetch/post/triage/report/next/show/check-tree/verify/doctor/calibrate + validation + store
  .env.example
```

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
#    Write exactly one .review/123/review.json {body, approved, comments[], resolved?, triage?}.

# 3. post (exactly once — validates anchors, merges findings, submits)
./scripts/review.sh post owner/repo 123
```

`review.json` schema:

```json
{
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

Inline rules: `path` must be in the diff, `line` = NEW-side line inside
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
Incremental runs read it and dedup by sig —
recorded findings are not re-reported unless the delta changes them. Before posting, `post`
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

## Learnings (durable repo memory)

`.review/learnings.md` at the repo root is the skill's only cross-run memory: durable
knowledge (test commands, conventions, gotchas) in `##`-headed sections. `fetch` reports
it when present; agents read headings first and challenge stale claims. Post-run reflection
appends durable lessons (never transcripts). Deleting a wrong entry beats editing around it.

## Machine surface

`fetch`/`post`/`triage`/`report`/`next`/`show`/`verify`/`doctor`/`calibrate` take `--json`: stdout is exactly one JSON envelope
(`schema_version`, `verb`, `repo`, `pr`, `at`, `next`, `warnings`, `data`, `error`),
all human progress goes to stderr, failures emit `{data: null, error: {code, message}}`
non-zero. `report` is the pure offline reader over the store (ranked items, verdict,
counts, patch passthrough, optional `--output` markdown with machine-header markers,
optional `--sarif` for scanners/CI). Severity is stored at write time (unknown → null,
prose emoji never parsed) — rank uses status, then severity, then rigor, then
anchored/inBounds, then path/line/sig. `doctor` preflights the harness; `calibrate`
joins findings against live ground truth, read-only. The sig algorithm (sha1 of
whitespace-squashed path+heading+body, line-number-free) is frozen: changing it
orphans history.

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

## Out of scope by design

Hosted settings, metered proxies, token mints, server-seeded comments and checks,
approval gating, auto-merge, cross-run persistence, upload signing, credential pools,
watch feeds, signed-commit flows. For on-demand local review none of these move
quality — the prompts do.

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

## Store changelog (additive within schema_version "1" — renames never happen, only additions)

- Initial: findings records `{sig, kind, path/heading, line, side, anchored, body}` + `status: open/addressed` + `firstSeen/lastSeen` + append-only `history`.
- Added: `rigor`, `inBounds`, retained `suggestion`, `rationale`/`triagedAt`/`triagedBy`, history `actor`/`sha`; statuses `wont-fix/false-positive/deferred`; `verification.json` evidence gate; `patch.json` gains `verifyCmd/verifyOutput/baseHead/resultHead`.
- Added: `verify` verb (executes `patch.json:verifyCmd`, records exit + tail); `.review/learnings.md`
  durable repo memory; red-lens adversarial framing; `in-bounds`/`severity` tags; self-authored downgrade.
- Added: `doctor` verb; `report --sarif`; `post.json` capture (calibration enabler).
- Added: `calibrate` verb (predictions vs ground truth, read-only, never auto-tunes).
