---
name: verdict
description: "Local on-demand code review for any git target — a GitHub PR via gh, another host with --provider, or local git (staged changes, a branch, a commit range, a stash entry, a worktree) — on any model. Triggers: review a PR, re-review new commits, review my staged changes, review this branch, review a commit range, address review threads, fix CI failures, report without posting."
---

# Local code review workflow

The agent does the review; `gh` does GitHub I/O. `gh` must be authenticated —
its own `gh auth login` or `GH_TOKEN`; `verdict doctor` checks. The model is
whatever agent you invoke.

## Where commands run

Run the CLI from the repo under review: `.review/` is created in the current
directory, never beside the tool. The primary path is the GLOBAL `verdict`
binary installed once (`curl -fsSL
https://stainless-code.com/verdict/install.sh
| sh`, `npm i -g @stainless-code/verdict`, or `brew install
stainless-code/tap/verdict`); the installed `verdict/` skill dir is only the
skill. Below, `verdict` means the resolved binary.

## Install or refresh this skill

The binary carries its own skill, so a copy can never skew from the CLI:
`verdict skill` prints this file, `verdict skill files` lists the payload, and
`verdict skill install [--agent <name>]` (or `--dir <skills-dir>`) writes it
atomically with a `.verdict-managed` receipt. Re-run it after upgrading the
binary.

A skill copy resolves the binary through `scripts/verdict`: `VERDICT_BIN`,
then `<skill>/bin/verdict`, then `verdict` on `PATH`. When none exists, install
the global binary (primary) or run the one-time verified `scripts/verdict
bootstrap` — it fetches and verifies once, then works offline; a review never
downloads. `npx skills` manages the skill copy only: `npx skills update`
refreshes it.

## Workflow

```bash
# 1. Fetch the PR bundle (diff + metadata, no clone needed)
verdict fetch owner/repo 123
# Done when .review/123/ holds PR.json, full.diff, toc.txt, roles.txt,
# controls.json. Review controls (.review/kb.jsonl, panel, budget, overrides,
# ...) are read from the PR base commit, never the PR head — from the clone,
# else the GitHub contents API; --allow-head-controls is the one recorded opt-out.
# Over-cap diffs refuse: re-run fetch with --chunk (or commit true in
# .review/chunk at the bundle base: the PR base for fetch, the local diff
# base (HEAD by default) for fetch-local), read each chunk per
# chunks/manifest.json, and stamp top-level "chunks" naming every manifest
# chunk in review.json.

# 2. Review with any agent. Load `prompts/system.md`, then the one mode file
#    from the router below, plus the bundle.
#    Write exactly one `.review/N/review.json`; the mode file defines its shape
#    (`body` and `comments[]` at minimum, plus optional `resolved` sigs and
#    `triage[]` claims).
#    Prior feedback lives in `.review/N/threads.json` (all six hosts): host-resolved
#    state, anchors, authors, and `finding_sig` links. `verdict threads owner/repo N`
#    refreshes it alone — no diff refetch — so run it after pushing fixes and before
#    a re-review or merge gate.

# 3. Post: validates anchors, merges the findings store, submits. Done when
#    exactly one review lands — one review.json, one post call.
verdict post owner/repo 123 --approve=false
```

Local changes (no PR): `verdict fetch-local owner/repo work
[--staged] [--range A..B] [--base R] [--stash [n]] [-- paths...]` writes
`.review/local-work/` (source.json instead of PR.json), the bundle is reviewed
the same way, and `verdict record owner/repo local-work` merges it
to `findings.json` — read with `report`, never `post` (no PR target). Before
recording, run `verdict record <repo> <slug> --dry-run`, fix every problem it
lists, repeat at most 3 rounds, and if problems remain stop and report them
rather than recording a partial review.
`adopt owner/repo 123 --from local-work` carries a local review into a fresh
PR fetch for posting. Downstream verbs address the bundle as `local-work`.

Never call `triage` — propose status changes via `triage[]` in `review.json`.
The operator's own reads between runs: `triage` (status changes), `report`
(ranked read), `threads` (refresh the PR conversation feed), `calibrate`
(predictions vs ground truth), and `fix` (one bounded verify-gated pass over a
local bundle — prints the finding plus land instructions).

Delta re-review: `verdict fetch owner/repo 123 --base-sha <prior-reviewed-sha>`,
then load `prompts/incremental-review.md` instead of `prompts/review.md`.

## Mode router

| Ask                                        | Load                                       | Terminal write                                           |
| ------------------------------------------ | ------------------------------------------ | -------------------------------------------------------- |
| Review a PR                                | `prompts/review.md`                        | one review                                               |
| Review local changes (no PR)               | `prompts/review.md` + `source.json` bundle | `record`, then `report` — never `post`                   |
| New commits landed                         | `prompts/incremental-review.md`            | one review OR no-review note                             |
| Address review threads                     | `prompts/address-reviews.md`               | commit/push + one reply per thread                       |
| Fix failing CI                             | `prompts/fix-ci.md`                        | commit/push + summary                                    |
| Implement / build                          | `prompts/build.md`                         | commit/push + PR link                                    |
| Draft a review report without posting      | `prompts/ci.md`                            | report markdown file, no post                            |
| Grade closed findings (self-grading probe) | `prompts/grade.md`                         | grade rows + agent judgments (additive) + agreement rate |
| Anything else                              | `prompts/system.md` + task text            | summary stdout                                           |

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
   `open` + `deferred` findings stand (`--force` overrides), and a binding `APPROVE`
   requested on a stale fetch (commits landed after fetch) posts as `COMMENT`, never
   `APPROVE` — `--force` does not override that; re-fetch the delta to bind.
6. **Line-count is not signal.** One-line auth/money/SQL/comparison/redirect/config-default
   changes earn full scrutiny; docs/whitespace/lockfile/generated/mechanical-rename diffs skip it.
7. **Batch aggressively.** Emit every independent read/grep in a single turn — turn count
   is cost. Commands run synchronously, so issue the next call immediately.
8. **Only posted output counts.** Raw assistant text exists in logs only. Posted output
   reads as a polished teammate's comment, never as thinking-out-loud.

There is no server to consult: the bundle in `.review/N/` plus what `gh` returns
are the whole evidence base for this run.

## Spend tracking (usage handoff, optional)

If you know this run's real usage — tokens in/out (plus cached), USD, and the
run window — write one usage record per `schemas/usage.schema.json` and report
its path. The operator hands it to the next `fetch` via
`VERDICT_USAGE=<file|dir>` so the spend budget prices from measured runs.
Otherwise write nothing: the budget estimate stays self-reported, never
guessed. Harness exports (Claude Code transcripts, opencode exports) are
outside the binary — convert them with
[`examples/usage-adapters/`](https://github.com/stainless-code/verdict/tree/main/examples/usage-adapters)
first; `verdict` never reads those files itself.
