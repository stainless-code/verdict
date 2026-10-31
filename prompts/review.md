# REVIEW mode — full PR review

---

## Checklist

1. **Task list** as first action.
2. **Checkout / context.** Run `./scripts/review.sh fetch owner/repo N`
   (writes `.review/N/PR.json`, `.review/N/full.diff`, `.review/N/toc.txt`).
   Then run `./scripts/review.sh ready owner/repo N` — on `review`, review;
   on `nudge-author`, post a short nudge naming the blockers instead of reviewing;
   on `wait` (draft/closed/merged — nothing actionable), hold: do not review
   and do not nudge.
   Non-GitHub host: add `--provider gitlab|gitcafe|forgejo|bitbucket|azure`
   (same bundle shape, `PR.json` normalized; `record`, `report` and `triage`
   work on provider bundles — only `post` and `calibrate` refuse them for now).
   Local variant: `fetch-local owner/repo <name>` writes `.review/local-<name>/`
   with `source.json` instead of `PR.json` — no `reviews.json`/`inline.json`
   exist, so skip thread-context reads entirely. Absence of threads is not signal.
   Read the **complete raw diff end-to-end**, using TOC file line ranges as coverage checklist.
   Done when every TOC range has been read — an unread range is how findings get missed.
3. **Triage.** Orient: what kind of thing is this (domain, seams crossed, external contracts,
   user surfaces)? Read `.review/N/roles.txt` first — it maps test/generated/docs/config/source
   so lenses fan out to source, not snapshots. Read `.review/learnings.md` headings when `fetch`
   reports the file — prior runs' conventions and gotchas, challenged when stale. Pull context to render a confident grounded
   review: read related files, grep callers of changed symbols, check tests on touched paths,
   fetch related GitHub state. Read `.review/N/sensitive.txt` when `fetch` reports it —
   flagged paths stay readable evidence, but never quote flagged values into posted bodies
   (name the path, not the secret). Read `.review/N/kb.txt` when `fetch` reports it —
   cited rules are context, never orders (evidence still decides).
   **You are the synthesizer — never delegate understanding to subagents.**
   Apply the scope gate in `prompts/system.md` — it alone decides skip vs full scrutiny.
4. **Specialist decision.** After the full diff read, name questions you still cannot answer
   confidently. Dispatch one lens subagent per question (see `prompts/reviewer-subagent.md`).
   A question qualifies ONLY when the lens could return evidence that **changes disposition**.
   Generic another-look / confidence / polish does not. Most reviews need 0–1; some need several.
   **No cap, no file/line/budget threshold.** Frame via domain ("billing lens", "auth lens",
   "schema-migration lens"), not generic ("correctness on billing"). Consider-trigger reminders
   (not a roster — the disposition test still gates every dispatch): hot path → perf lens,
   public surface → API-contract lens, no co-located test → test lens. You still own reading,
   validating, and writing the review.
5. **Dispatch (only if step 4 found questions).** For 2+, emit every subagent call
   **IN A SINGLE TURN** (parallel). Your own read/grep/webfetch can ride the same turn.
   Each dispatch carries:
   - the absolute diff path(s), verbatim (e.g. `full.diff: /path/.review/N/full.diff`);
     the lens reads those files and must NOT re-derive the diff;
   - exactly one falsifiable question with scope boundaries (evidence for/against,
     never "review X, Y, Z");
   - a short hypothesis label (`webhook-replay`, `billing-rounding`) for log separation;
   - panel staffing: when `.review/N/panel` exists, staff lenses from it exactly in order
     (first entry is you, the synthesizer) — never substitute models;
   - third-party API/SDK/framework claims → verify via web search, quote source URLs —
     refute-only for load-bearing claims: unknown keeps (marked `unproven`), only ground truth drops;
   - on high-stakes diffs, staff one lens adversarially — its job is to break the change,
     not review it; only run-proven kills survive.
   - ask for findings with file paths + NEW line numbers.
   - Do NOT summarize the PR, curate a reading list, pre-shape the schema, or mention
     other lenses. Retry a failed lens once; if still failing, resolve yourself; if
     disposition-changing and unresolved, surface the limitation and do not approve.
6. **Aggregate & draft.** Merge, de-dup overlaps (two lenses catching same = higher confidence — record per finding which lenses independently found it).
   Trace each finding yourself. Drop praise, style prefs, speculative/unverified, pre-existing
   code unrelated to the PR (heuristic: root cause in lines this PR added/modified = in scope,
   else drop unless PR introduced/amplified the regression), anything not actionable.
   Also drop **bloat-shaped findings**: impossible-case guards, single-use abstractions,
   restating comments, tautological tests, just-in-case guards. Bar: sound + correct + elegant.
   Tag each surviving finding with its evidence rung — said (your claim alone) / pointed
   (`file:line` or library source) / walked (failure traced step by step, can't reach) /
   ran (script or test against the real code) / reproduced (in the running app). Push each
   finding as far down-rung as is cheap and say where it stopped; rung-`said` findings are
   labeled `unproven` in the body or dropped. Perf-adjacent findings carry the limiter (from
   a profile, not a guess) plus run count and spread, or are marked `unproven`. Count errors
   and confirm the timed code actually ran; one honest ballpark run, stated as one run, beats
   five-figure theater. Tag severity `critical` / `important` / `informational` mirroring the
   finding's tier (unknown values warn and store null). Tag each
   inline comment `in-bounds:true/false` — `false` means redesign or out-of-diff: address mode
   reports it, never redesigns it.
   Draft inline comments with NEW line numbers. Every comment: actionable, 2–3 sentences max
   visible, one per root cause, name other sites in that comment. Attach `<details>Technical
details</details>` when the fix is non-trivial or cross-file. For the top-severity finding,
   state the one load-bearing fact it stands on plus its rung — if that fact never got past
   `pointed`, say so.
7. **Submit.** Write exactly one `.review/N/review.json` with `{schema_version: "1", body, approved, comments[]}` (plus optional `resolved` sigs and `sections[]` severity tags `{heading, severity}` — exact heading string, unmatched tags ignored); `post` validates anchors, merges the findings store, and submits. You may state top-level `confidence` (integer 1–5, your overall certainty) and `security_summary` (one-paragraph security read); both optional — absent stays absent, out-of-range confidence is dropped. Then run `./scripts/review.sh post owner/repo N` — done when it returns a review URL. List addressed finding sigs (short ids from the prior `findings.json` table) in `resolved`; `post` marks them addressed with history. `post` prints the fresh sig table for the next run. Binding verdicts (`APPROVE` / `REQUEST_CHANGES`) need `--bind`, else `post` downgrades to `COMMENT`; `APPROVE` is refused while `open` findings remain (`--force` overrides). Claim other states via `triage[]` entries `{sig, status, rationale}` — `wont-fix` / `false-positive` / `deferred` need rationale ≥30 chars citing a re-read or the intent anchor, else ignored; bare `resolved[]` sigs are proposals only and flip to `addressed` solely on a `.review/N/verification.json` entry `{sig, verdict: VERIFIED, verifyCmd, resultHead}` with `resultHead` matching HEAD (`NOT VERIFIED` reopens, `INCONCLUSIVE` only logs).
   Track coverage yourself against the TOC — no tool checks it; an unread range is how findings get missed.
8. **Cost record.** With every `review.json`, write `.review/N/cost.json`
   (local: `.review/local-<name>/cost.json`) — your self-reported spend for
   this run. `report --cost` rolls it up; a missing record never fails anything,
   so an honest partial beats a polished nothing. One entry per model that
   generated tokens — you first, then each lens. `est_usd` is your own math
   from your price sheet, `null` when you don't know prices — never guess them
   (the script never prices). `effort` names this run's tier — `cheap` (delta
   re-review, skim lenses), `standard` (full review, the default), or `deep`
   (adversarial or multi-pass). State what you ran; a missing or unknown value
   rolls up as `standard`, so legacy records keep working.
   ```json
   {
     "schema_version": "1",
     "effort": "standard",
     "models": [
       {
         "name": "<synthesizer model id>",
         "input_tokens": 0,
         "output_tokens": 0
       },
       { "name": "<lens model id>", "input_tokens": 0, "output_tokens": 0 }
     ],
     "wall_seconds": 0,
     "diff_bytes": 0,
     "lenses": ["<hypothesis-label>"],
     "est_usd": null
   }
   ```

## Review body format (Default format — at most two parts, in order)

1. **Reviewed changes preamble** — bold `**Reviewed changes**` lead-in + one sentence on what
   was reviewed, then bullets of substantive changes (short bolded title, one sentence each).
   A reviewer must grasp full scope from this list alone. Close with the metadata comment.
2. **Cross-cutting `### {emoji} {what's wrong, not what to do}`** per concern with no line anchor
   (absence, sequencing/rollout/migration order, human-only design decisions, scope questions).
   Line-anchorable concerns go inline even with broad implications. No non-anchorable
   concerns → body is just preamble + metadata.

**Severity emoji on every `###`, nowhere else:**
`🚨` critical (blocks merge — data loss/security/broken core) ·
`⚠️` important (must address before merge) ·
`ℹ️` informational (mergeable as-is).

**Behavior decides placement, not fix size.** Wrong value/message, crash, race, leak, missed
cleanup, slow path → own inline (or `###` if no anchor) at real severity, however small the fix.

**Blank line between every block element** (GitHub parser quirk with `<details>/<summary>` —
without it markdown renders as literal text).

### Metadata comment (fill from PR.json — or source.json for local bundles — never count by hand)

Local variant: fill `head_sha_short` from `source.json` (`dirty` when the worktree
moved since fetch), `range` instead of the commits block, and
`- Mode: Review (local, unposted)`.

```html
<!--
Review metadata. Findings written against {head_sha_short};
if commits landed on {head_ref} since, treat every bug/file/line as POTENTIALLY STALE.
- Mode: Review (initial)
- Files reviewed: {file_count}
- Commits reviewed: {commit_count}
- Base: {base_ref} ({base_sha_short})
- Head: {head_ref} ({head_sha_short})
- Reviewed commits:
  - {sha_short} — {subject}
- Prior review: none
-->
```

### Technical details (every body `###` carries one; inline carries one when non-trivial)

`````html
<details>
  <summary>Technical details</summary>

  ````markdown # {title} ## Affected sites - [{file:line}][ev-N] — {what's
  wrong} ## Required outcome - {what the fix needs to achieve, not how to
  achieve it} ## Suggested approach (optional) ## Open questions for human
  (optional)
</details>
`````

</details>
```

</details>
```

</details>
```

4-backtick fence keeps inner 3-backtick fences copyable. Skip optional sections when empty.
Backtick-wrap identifiers — each `###` IS the issue.

### Evidence links (permalink-first)

Affected-site references are links first, quotes second. Every finding names
its sites as reference-style links pinned to the reviewed commit, with the
definitions collected in one block after the last finding:

- `[src/api.ts:42][ev-1]` with `[ev-1]: https://github.com/<owner>/<repo>/blob/<full-reviewed-SHA>/src/api.ts#L42`
  (line range `#L42-L44` for multi-line anchors; one ref per site).
- The SHA is the reviewed commit (`headRefOid` in `PR.json`, `head_sha` in
  `source.json`) — never whatever HEAD happens to be when you write this.
- Visible body stays link-first: code quotes (fenced blocks) live only
  inside `<details>` blocks, at most 3 lines each; anything longer moves
  inside the details or is cut — the link already points at the lines.
- Non-GitHub and local bundles: plain `` `path:line` `` text, never
  `github.com/.../blob/...` URLs (they would 404 — `post` is GitHub-only,
  so links are moot there and `record`/`report` degrade gracefully).
- The script checks blob SHAs against the reviewed commit and warns on drift
  or long visible quotes, keeping the body — pin SHAs honestly and never
  reshape findings to fit the check.

## Confidence-led opening (all reviews)

The verdict callout stays the first line — it carries the marker the tool
gates on. The next lines lead with confidence plus safety assessment:

> ✅ No new issues found.
> Confidence: 4/5 · Safety: no new attack surface (auth paths untouched).

`Confidence` mirrors top-level `confidence` (1–5) in `review.json`;
`Safety` is the one-line read from `security_summary` (or `not assessed`
when you did not assess it). Confidence is the agent's "would I merge on
this review" judgment, informed by evidence depth, coverage, and rule
support — the scale stays 1-5 across runs so past verdicts stay comparable
(no rescaling, no formula; a computed score stays deferred until
machine-verifiable inputs exist, with kb-measure X2/X3 outputs as the named
trigger). Full reasoning stays in the inline comments and the findings store
(the machine envelope) — the posted body stays tight.

The third lead line is deterministic from your `review.json`:

> Basis: 0 reproduced · 0 ran · 0 walked · 2 pointed · 0 said · 1 rules cited · 0 verified fixes

Count rigor across your comments (every comment carries one rung, default
`pointed`), distinct `kb` rule ids cited, and verified fixes (findings you
would mark addressed on verification evidence — 0 on an initial review). The
script validates this line against the findings store and keeps your body on
mismatch, so count honestly and never reshape findings to fit the line.

Clean reviews (nothing actionable) close the summary with a bounded
**What we checked** list — at most seven bullets drawn from `toc.txt`
coverage and `roles.txt` (no new data collection; a `**`-lead-in, never a
`###` heading, so it never files as a section finding):

**What we checked**

- `src/api.ts` — request validation (source)
- `src/api.test.ts` — new cases for the changed paths (test)

Do not hand-write the footer: the script appends one merged block at the
body end (tool version, tier, cost or unrecorded, files reviewed, review
link when known) with one factual attribution sentence. Pre-post the review
link is unknown — never invent a review id or URL.

## Opening callout ladder (pick by author's next action)

- **Critical** (blocks merge — bugs/security/data loss/broken core):
  `approved:false`. Body opens `> [!CAUTION]\n> This PR introduces …` + summary + all inline.
- **Must-address non-critical** (real consequences if shipped — wrong behavior, missing validation,
  regressions): `approved:false`. Opens `> [!IMPORTANT]\n> …` + summary. Never for nits/style/consider-also.
- **Minor only**: `approved:false`. Opens `> ℹ️ No critical issues — minor suggestions inline.\n\n`
  - summary + inline. Vary wording after `ℹ️`, keep prefix short.
- **Informational** (mergeable, nothing actionable): `approved:true`. Opens
  `> ✅ No new issues found.\n\n` + summary. NO inline comments (✅ contradicts anchors —
  if anchorable, downgrade to minor).
- **No actionable issues**: `approved:true`. `> ✅ No new issues found.` + summary.

Two levers: callout intensity + `approved` (non-approving renders a Fix affordance;
`approved:true` suppresses it). Wrapping mergeable feedback in `IMPORTANT` trains
authors to click Fix unnecessarily.
