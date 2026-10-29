---
name: verdict
description: "Local on-demand PR review via gh CLI, any model. Triggers: review a PR, re-review new commits, address review threads, fix CI failures, report without posting."
---

# Local PR review workflow

The agent does the review; `gh` does GitHub I/O. You bring `GH_TOKEN`;
the model is whatever agent you invoke.

## Workflow

```bash
# 1. Fetch the PR bundle (diff + metadata, no clone needed)
./scripts/review.sh fetch owner/repo 123
# Done when .review/123/ holds PR.json, full.diff, toc.txt, roles.txt.

# 2. Review with any agent. Load prompts/system.md, then the one mode file
#    from the router below, plus the bundle. Write exactly one `.review/N/review.json` with `{schema_version: "1", body, approved, comments[]}` (plus optional `resolved` sigs and `triage[]` claims `{sig, status, rationale}`); `post` validates anchors, merges the findings store, and submits. The mode file defines the exact shape.

# 3. Post. Done when exactly one review lands — one review.json, one post call.
./scripts/review.sh post owner/repo 123 --approve=false
```

Local changes (no PR): `./scripts/review.sh fetch-local owner/repo work
[--staged] [--range A..B] [--base R] [--stash [n]] [-- paths...]` writes
`.review/local-work/` (source.json instead of PR.json), the bundle is reviewed
the same way, and `./scripts/review.sh record owner/repo local-work` merges it
to `findings.json` — read with `report`, never `post` (no PR target).
`adopt owner/repo 123 --from local-work` carries a local review into a fresh
PR fetch for posting. Downstream verbs address the bundle as `local-work`.

Between runs the operator may run `./scripts/review.sh triage …` (status changes), `report`
(ranked read), or `calibrate` (predictions vs ground truth); agents never call `triage` — they propose via `triage[]`.

Delta re-review: `./scripts/review.sh fetch owner/repo 123 --base-sha <prior-reviewed-sha>`,
then load `prompts/incremental-review.md` instead of `prompts/review.md`.

## Mode router

| Ask                                   | Load                                       | Terminal write                         |
| ------------------------------------- | ------------------------------------------ | -------------------------------------- |
| Review a PR                           | `prompts/review.md`                        | one review                             |
| Review local changes (no PR)          | `prompts/review.md` + `source.json` bundle | `record`, then `report` — never `post` |
| New commits landed                    | `prompts/incremental-review.md`            | one review OR no-review note           |
| Address review threads                | `prompts/address-reviews.md`               | commit/push + one reply per thread     |
| Fix failing CI                        | `prompts/fix-ci.md`                        | commit/push + summary                  |
| Implement / build                     | `prompts/build.md`                         | commit/push + PR link                  |
| Draft a review report without posting | `prompts/ci.md`                            | report markdown file, no post          |
| Anything else                         | `prompts/system.md` + task text            | summary stdout                         |

`prompts/system.md` loads on every run. `prompts/reviewer-subagent.md` is the
contract for lens subagents the Review modes dispatch — the orchestrator reads it
to write dispatches, lenses run under it.

## Core rules (do not soften)

1. **You are the synthesizer.** Read the complete raw diff end-to-end first, using the
   TOC ranges as coverage checklist. Lenses supplement that reading; they never replace it.
2. **Fan-out is hypothesis-driven, not capped.** 0–N lenses, one falsifiable question
   each, dispatched in parallel. A question earns a lens only if its answer could change
   your verdict. Staff from `.review/N/panel` when `fetch` wrote one — same panel every
   run keeps verdicts comparable and costs capped.
3. **Bar is sound + correct + elegant.** Keep findings that clear all three; tag each with
   its evidence rung (said/pointed/walked/ran/reproduced — scale lives in `review.md`); drop praise,
   style prefs, unverified claims, pre-existing code the PR didn't touch, and bloat-shaped
   fixes (impossible-case guards, single-use abstractions, restating comments, tautological tests).
4. **Exactly one terminal write.** The router's Terminal-write column is exhaustive:
   produce that write and stop. A trivial incremental delta ends in a 1–2 sentence
   no-review note, never an empty review.
5. **Match the callout to the author's next action.** The severity ladder lives in
   `prompts/review.md` — pick the tier the author should act on, never louder. Binding
   verdicts need `--bind` (else `post` downgrades to `COMMENT`); `APPROVE` is refused while
   findings stand open (`--force` overrides).
6. **Line-count is not signal.** One-line auth/money/SQL/comparison/redirect/config-default
   changes earn full scrutiny; docs/whitespace/lockfile/generated/mechanical-rename diffs skip it.
7. **Batch aggressively.** Emit every independent read/grep in a single turn — turn count
   is cost. Commands run synchronously, so issue the next call immediately.
8. **Only posted output counts.** Raw assistant text exists in logs only. Posted output
   reads as a polished teammate's comment, never as thinking-out-loud.

There is no server side: anything not in this folder or reachable via `gh` doesn't
exist for this run — don't look for it.
