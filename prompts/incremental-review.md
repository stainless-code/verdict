# INCREMENTAL REVIEW — delta re-review after new commits

Same body format + callout ladder as `review.md`, scoped to the delta.

---

## Checklist

1. **Task list** first.
2. **Checkout.** `./scripts/review.sh fetch owner/repo N --base-sha <prior-reviewed-sha>`
   (writes `full.diff` + `incremental.diff` when computable). If `incremental.diff` exists it is
   your coverage checklist: read it FIRST in full, then only the `full.diff` ranges the delta
   needs for context. If absent: full TOC, determine what changed since the last review yourself.
   Also read `.review/N/findings.json` (prior sigs, statuses, history) — it feeds step 6 dedup.
   Chunked bundles: when the bundle carries `chunks/manifest.json`, the delta is scoped to the
   affected chunks. Map every `incremental.diff` file to its manifest chunk(s); those chunk diffs
   (`chunks/chunk-NN.diff`, manifest order) are your coverage checklist — read each affected chunk
   diff end-to-end against its `chunk-NN.toc.txt`, plus `full.diff` ranges only for context the
   delta needs. Unaffected chunks are out of scope: never re-review the whole bundle. If
   `incremental.diff` is absent, determine the delta yourself the same way, then scope to the
   chunks it touches.
3. **Prior feedback — read AND retire it**: fetch previous reviews via `gh api repos/{o}/{r}/pulls/{n}/reviews`, then thread
   comments per prior review. Triage open threads severity-first (blocking → must-fix → minor).
   For every **open, bot-originated** thread: read the file at the
   anchor, judge whether new commits substantively resolve it (reformat/rename/move ≠ addressed;
   multi-concern threads need ALL addressed; `[OUTDATED]` anchor-shift ≠ addressed — re-read
   new location). If addressed: reply one line (`Addressed in <sha>.`) then resolve — BEFORE
   drafting the new review. If uncertain/partial: leave open. Only retire bot threads; human
   threads belong to humans. False-positive resolves erode trust faster than false negatives.
   Carry still-open out-of-scope items as Deferred bullets in the review body — what, and why deferred.
4. **Triage the incremental changes** (domain/seams/contracts/surfaces). You are the synthesizer.
   Apply the scope gate in `prompts/system.md` — it alone decides the non-substantive path
   (no review, 1–2 sentence note) vs full scrutiny.
5. **Specialist decision** on the new changes (same disposition-changing test as Review, no cap,
   domain framing). Name BOTH diff paths in each dispatch when incremental exists (on chunked
    bundles, name the affected chunk diff paths the same way — the lens contract is unchanged).
   Do NOT tell lenses to skip pre-existing issues — that suppresses regressions new commits
   amplified. The "must be NEW" filter lives at aggregation, not in the lens prompt.
   Staff lenses from `.review/N/panel` exactly as in `review.md` step 5 when it exists.
   Default to cheap lenses on delta re-reviews; escalate only when the
   disposition test demands a stronger model. Record the run as
   `"effort": "cheap"` in `cost.json` unless you escalated.
6. **Aggregate / self-critique.** Merge yours + lenses, de-dup, trace each yourself.
   Drop praise/style/speculative/pre-existing-unrelated/re-stated-prior-feedback/not-actionable/
   bloat-shaped (same sound+correct+elegant bar). Tag survivors with the evidence-rung scale
   from `review.md` (unchanged on chunked bundles: chunking moves evidence, never manufactures
   it; calibration grades the same scale). Tag inline `in-bounds` per `review.md`. Dedup against `.review/N/findings.json` by
   sig — an already-recorded open finding is not re-reported unless the delta changes it.
   "New lines": `incremental.diff` directly,
   else `git diff <prior-review-sha>..HEAD`. Draft inline with NEW numbers from full diff.
   Sigs are line-number-free, so chunk re-slicing never breaks dedup on chunked bundles.
7. **Body.** Same Default format as Review, scoped to the **delta** ("Reviewed changes" bullets
   describe since prior, each starting with a past-tense verb, e.g. `- Extracted shared CLI…`).
   No separate "Prior review feedback" checklist — that lives in your working notes, not the body.
8. **Submit — EXACTLY ONE of:**
   - No new issues + non-substantive delta → NO review. Write `.review/N/noreview.md` (1–2 sentences,
     e.g. "No new issues. Changes since last review are formatting-only.") and post as a plain
     issue comment (or stdout if local-only). Do NOT submit an empty review.
   - New critical → `approved:false` + `> [!CAUTION]` + summary.
   - New must-address non-critical → `approved:false` + `> [!IMPORTANT]` + summary (never for nits).
   - New minor only → `approved:false` + `> ℹ️ …` + summary.
   - Informational (mergeable, worth surfacing) → `approved:true`, NO inline, `> ✅ …` + summary.
     Anchorable point → downgrade to minor instead.
   - No new issues + substantive delta (new behavior/fixes) → review, `approved:true` when prior
     feedback is addressed, body `> ✅ No new issues found.` + summary.

Every review branch above ends the same way: Write exactly one `.review/N/review.json` with `{schema_version: "1", body, approved, comments[]}` (plus optional `resolved` sigs); `post` validates anchors, merges the findings store, and submits — the same path as an unchunked delta, no extra machinery. On chunked bundles the cross-chunk coverage gate applies unchanged: stamp every manifest chunk in top-level `"chunks"` — the affected chunks re-read for this delta, plus the unaffected chunks carried from the prior completed review (prior `findings.json`, step 2; when no prior review exists, read the unaffected chunks too) — and a synthesis missing one is refused, never posted partial. Top-level `confidence` (1–5) and `security_summary` are optional, as in `review.md` — absent stays absent. List prior-run sigs the delta addresses in `resolved`. Triage and verification work exactly as in `review.md` step 7.

Coverage is yours to track against the TOC — no tool checks it; read every range the delta touches. On chunked bundles, track it against the affected chunks' checklists instead — an unread affected chunk is as visible as an unread range.
