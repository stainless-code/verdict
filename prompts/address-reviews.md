# ADDRESS REVIEWS — respond to review threads

1. Task list first. `gh pr checkout N` (or work in existing checkout).
   Record the intent anchor first: stated intent (PR title/body or user ask), commit range,
   and HEAD sha; ask "What must not change?" when intent is unclear. Cap of 3 passes per
   invocation — pass 1 fixes everything in-bounds, passes 2+ blockers-then-nits; on hitting
   the cap with work left, stop and list Deferred bullets instead of continuing.
2. Fetch threads: `gh api repos/{o}/{r}/pulls/{n}/comments` + full conversation per thread.
3. Per thread: **intent first** — questions/explanations/disagreement call for a reply, not code.
   Change code only when explicitly requested. **Verify each finding against the code** — every
   comment (human or agent) is a hypothesis, not a directive. Re-read the cited file:line
   before applying any finding — drop false or by-design on sight. Keep changes sound, minimal,
   elegant. Fix at the level that removes the cause, not the symptom — chain why until the
   answer is a wrong decision in the code; fix every instance of the same fault, not just the
   reported one. A workaround needing a paragraph-long justification means the code is wrong —
   fix the code, not the comment. Reject impossible-case guards, single-use abstractions, redundant comments,
   tautological tests. Implement with native tools or reply without editing; record done/why-not.
4. Preview first: show the file list and diff stat of what you would change, then proceed
   (stop before push on request). Require a clean tree before editing — run
   `./scripts/review.sh check-tree`; it refuses on unexpected dirt. Commit or stash anything unrelated first.
5. Quality: re-fetch comments before finishing even when only replying. If code
   changed: test, review own diff, remove bloat, confirm every dirty file maps to an
   addressed thread or explicit request (stop-and-report on hitchhikers — never auto-revert),
   record `.review/N/patch.json`
   (`{findingSig, filesChanged[], commandsRun[], verifyCmd, verifyOutput, baseHead, resultHead}` from `git diff --name-only` before/after;
   `verifyCmd` is the exact rerun proving the fix, `verifyOutput` its tail,
   `baseHead`/`resultHead` bracket verification),
   commit (`git add && git commit`), push.
6. Uncertain-by-default: your fix pass never marks a thread fixed. Dispatch one read-only
   lens as revalidator against the pushed code; only it may resolve threads, and only on
   verified-fixed. The revalidator reports one verdict per thread — VERIFIED (with command +
   output) / NOT VERIFIED / INCONCLUSIVE. INCONCLUSIVE never resolves — it replies without
   resolving. Hand it `patch.json`'s `verifyCmd` + `filesChanged` as the lead; VERIFIED still
   requires its own independent re-run. It appends one `{sig, verdict, verifyCmd, resultHead}`
   entry per thread to `.review/N/verification.json` (creating it); only `VERIFIED` with a
   matching HEAD flips `addressed`. Prefer a machine `verify` result when present. Otherwise reply without resolving.
7. Finalize ONLY if code changed → land it as a single commit (amend as you iterate, push
   once at the end); on push failure report exact error and STOP (don't
   claim fix or resolve threads). Reply ONCE per relevant thread (root numeric `id`, not thread
   GraphQL id). Log every posted reply `{thread, reply_id, at}` into `.review/N/replies.json`
   (creating it) and skip threads already logged — a crashed-and-rerun pass must never
   double-reply. Resolve only after the revalidator confirms fixed,
   user asked, or you withdraw your own bot finding. Leave discussion/partial/disagreement open.
8. Summary once: open with a 1–2 sentence intent restatement (what you understood the goal
   to be), then what changed + links. Tag each thread `[fixed <sha>]` / `[open]` / `[partial]`
   from the last fetch. Name unaddressed threads found in the last fetch —
   a late-arriving review is otherwise lost. If capped, lead with Deferred bullets (what + why deferred).
