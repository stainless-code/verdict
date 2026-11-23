---
"@stainless-code/verdict": patch
---

Panel receipts use version 2 with invoker-assigned checker identities. S5 receipts bind the execution, head, diff, frozen review and dispatch plan, and every confirmation or refutation quote passes the S4 matcher. Approval, spec, compliance, post, report, gate and ready require all assigned other-finder checks. Missing or disputed blocking checks withhold approval as insufficient. Manual panel decisions remain unchanged when no S5 execution is captured.

This is a deliberate pre-v1 break — version 1 panel receipts are refused, with no compatibility decoder. Finder review input remains version 1; execution-bound aggregate review input uses version 2. Execution fields are inactive until a coordinator captures an S5 execution.
Stored finder receipts now use the largest completed count across `record.json`
and `post.json`; a captured one-finder policy in `run.json`'s resolved defaults
takes precedence over stale two-finder receipts when its top-level count is zero.

Changed schemas (with matching generated baselines and semantic pins):

- `schemas/envelope.schema.json`
- `schemas/findings.schema.json`
- `schemas/panel-plan.schema.json`
- `schemas/panel.schema.json`
- `schemas/post.schema.json`
- `schemas/record.schema.json`
- `schemas/review.schema.json`

The verifier prompt now states the v2 contract. These goldens change only prompt hashes, their derived run IDs and procedure receipt hashes/byte counts:

- `internal/fetch/testdata/author-advisory.golden`
- `internal/fetch/testdata/azure-happy.golden`
- `internal/fetch/testdata/base-sha-json.golden`
- `internal/fetch/testdata/base-sha-note.golden`
- `internal/fetch/testdata/bitbucket-happy.golden`
- `internal/fetch/testdata/bitbucket-paging.golden`
- `internal/fetch/testdata/budget-over-consented.golden`
- `internal/fetch/testdata/budget-unknown.golden`
- `internal/fetch/testdata/budget-unreadable.golden`
- `internal/fetch/testdata/budget-unset.golden`
- `internal/fetch/testdata/budget-within.golden`
- `internal/fetch/testdata/cap-override-beats-chunk.golden`
- `internal/fetch/testdata/cap-override.golden`
- `internal/fetch/testdata/controls-base-absent.golden`
- `internal/fetch/testdata/dump-json.golden`
- `internal/fetch/testdata/dump-text.golden`
- `internal/fetch/testdata/empty-diff.golden`
- `internal/fetch/testdata/file-paging.golden`
- `internal/fetch/testdata/forgejo-happy.golden`
- `internal/fetch/testdata/gitcafe-base-note.golden`
- `internal/fetch/testdata/gitcafe-happy.golden`
- `internal/fetch/testdata/gitlab-base-note.golden`
- `internal/fetch/testdata/gitlab-base.golden`
- `internal/fetch/testdata/gitlab-happy.golden`
- `internal/fetch/testdata/gitlab-text.golden`
- `internal/fetch/testdata/happy-json.golden`
- `internal/fetch/testdata/happy-text.golden`
- `internal/fetch/testdata/local-branch.golden`
- `internal/fetch/testdata/local-dump-chunk.golden`
- `internal/fetch/testdata/local-dump-text.golden`
- `internal/fetch/testdata/local-paths.golden`
- `internal/fetch/testdata/local-range.golden`
- `internal/fetch/testdata/local-range3.golden`
- `internal/fetch/testdata/local-staged.golden`
- `internal/fetch/testdata/local-stash.golden`
- `internal/fetch/testdata/local-untracked.golden`
- `internal/fetch/testdata/local-worktree-text.golden`
- `internal/fetch/testdata/local-worktree.golden`
- `internal/fetch/testdata/over-cap-chunk-text.golden`
- `internal/fetch/testdata/over-cap-chunk.golden`
- `internal/fetch/testdata/over-cap-knob.golden`
- `internal/fetch/testdata/ratelimit-retry.golden`
- `internal/fetch/testdata/sensitive-kb-head.golden`
- `internal/fetch/testdata/sensitive-kb.golden`
- `internal/fetch/testdata/since-alias.golden`
- `internal/fetch/testdata/threads-degraded.golden`
- `internal/record/testdata/body/mermaid-stripped.golden`
- `internal/record/testdata/body/non-ascii.golden`
- `internal/record/testdata/body/receipt-collision.golden`
- `internal/record/testdata/chunk/drift.golden`
- `internal/record/testdata/chunk/unattested.golden`
- `internal/record/testdata/chunk/unknown-chunk-claim.golden`
- `internal/record/testdata/chunk/valid.golden`
- `internal/record/testdata/claims/verify-triage-resolved/rereview.golden`
- `internal/record/testdata/degenerate/blob-link-local.golden`
- `internal/record/testdata/degenerate/brief-valid.golden`
- `internal/record/testdata/degenerate/carried-footer.golden`
- `internal/record/testdata/degenerate/fenced-suggestion.golden`
- `internal/record/testdata/degenerate/finding-confidence-valid.golden`
- `internal/record/testdata/degenerate/normalized-fenced.golden`
- `internal/record/testdata/degenerate/normalized-prose.golden`
- `internal/record/testdata/degenerate/normalized-weak-shapes.golden`
- `internal/record/testdata/incremental/non-touching/rereview.golden`
- `internal/record/testdata/incremental/reopen/rereview.golden`
- `internal/record/testdata/kb/absent.golden`
- `internal/record/testdata/kb/bad-id-shape.golden`
- `internal/record/testdata/kb/bad-policy-env.golden`
- `internal/record/testdata/kb/bad-shape.golden`
- `internal/record/testdata/kb/broken-store.golden`
- `internal/record/testdata/kb/counter-adds/rereview.golden`
- `internal/record/testdata/kb/duplicate.golden`
- `internal/record/testdata/kb/empty.golden`
- `internal/record/testdata/kb/false-positive/rereview.golden`
- `internal/record/testdata/kb/joins-idempotent/rereview.golden`
- `internal/record/testdata/kb/no-store.golden`
- `internal/record/testdata/kb/null.golden`
- `internal/record/testdata/kb/retired.golden`
- `internal/record/testdata/kb/string-counters/rereview.golden`
- `internal/record/testdata/kb/truncated.golden`
- `internal/record/testdata/kb/unknown.golden`
- `internal/record/testdata/kb/valid.golden`
- `internal/record/testdata/matrix/n10/first.golden`
- `internal/record/testdata/matrix/n10/rereview.golden`
- `internal/record/testdata/matrix/n200/first.golden`
- `internal/record/testdata/matrix/n200/rereview.golden`
- `internal/record/testdata/matrix/n50/first.golden`
- `internal/record/testdata/matrix/n50/rereview.golden`
- `internal/record/testdata/overrides/dir-as-fragment.golden`
- `internal/record/testdata/overrides/eq-threshold.golden`
- `internal/record/testdata/overrides/valid.golden`
- `internal/record/testdata/provider/gitlab.golden`
- `internal/record/testdata/refuse/empty-prior.golden`
- `internal/record/testdata/refuse/false-prior.golden`
- `internal/record/testdata/refuse/null-prior.golden`
- `internal/record/testdata/refuse/utf8-scrubbed-claim.golden`
- `internal/record/testdata/source/dirty-head.golden`
- `internal/record/testdata/source/dirty-string.golden`
- `internal/record/testdata/uncite/absent-keeps/rereview.golden`
- `internal/record/testdata/uncite/explicit-clears/rereview.golden`
- `internal/record/testdata/uncite/malformed-clears/rereview.golden`
- `internal/record/testdata/uncite/null-keeps/rereview.golden`
- `internal/record/testdata/verify/ambiguous-prefix/rereview.golden`
- `internal/record/testdata/verify/bad-head/rereview.golden`
- `internal/record/testdata/verify/contradict-exit/rereview.golden`
- `internal/record/testdata/verify/contradict-timeout/rereview.golden`
- `internal/record/testdata/verify/empty-cmd/rereview.golden`
- `internal/record/testdata/verify/inconclusive/rereview.golden`
- `internal/record/testdata/verify/on-addressed/rereview.golden`

Migrated eval fixture: `evals/cases/panel.sh`.

The generated TypeScript `SpecCheck` type in `clients/ts/src/types.gen.ts` adds optional `finding_sig` from the schema. Public token totals and `pkg/verdict/api.txt` are unchanged.
