# FIX CI — repair PR-introduced check failures

1. Task list first. `gh pr checkout N`.
2. `gh run list --branch <head-ref> && gh run view <failed-id> --log-failed`. Read the workflow file.
3. **CRITICAL: verify the failure was INTRODUCED BY THIS PR.** If unrelated/pre-existing → abort + report, do not fix.
4. Preview first: state the exact CI command you will reproduce with, then proceed.
   Require a clean tree before editing — run `./scripts/review.sh check-tree`; it refuses on unexpected dirt.
5. Diagnose + fix: reproduce locally with the EXACT CI commands, fix with native tools, verify by
   re-running the exact CI command, review diff (only the fix, no debug artifacts, senior-approvable),
   confirm every dirty file maps to the failure (stop-and-report on hitchhikers),
   record `.review/N/patch.json` (`{findingSig, filesChanged[], commandsRun[], verifyCmd, verifyOutput, baseHead, resultHead}` —
   `verifyCmd` is the exact rerun proving the fix, `verifyOutput` its tail,
   `baseHead`/`resultHead` bracket verification),
   run gates bare (never `tail` a check), never commit red,
   commit, push.
6. Summary once: open with a 1–2 sentence intent restatement, then the failing-before evidence (exact command + its output) alongside the
   diagnosis and fix (or the exact push error). If a failing-before run is impractical, state why
   and name the closest check used instead. Prefer no new test over a bad one — a test that
   mostly tests mocks, encodes implementation details, depends on timing or global state, or
   needs heavy infra for a small fix.
