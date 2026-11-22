# Over-broad or missing token permissions

The `GITHUB_TOKEN` is the workflow's identity. In a privileged trigger a
write-capable token lets a compromised step push commits, edit releases, or
approve pull requests; a least-privilege `permissions:` block bounds that
blast radius.

## When to flag

- `permissions: write-all` at the workflow level, or `permissions:` with
  every scope set to `write`.
- A missing `permissions:` block on a `pull_request_target`/`workflow_run`
  workflow, or on a workflow whose steps run untrusted code, so the token
  carries the repository's default (which may be read/write).
- A job-level `permissions` broader than the job's steps: `contents: write`
  on a job that only checks out, `pull-requests: write` on a job that only
  reads, `id-token: write` with no OIDC exchange.
- `contents: write` plus `pull_request_target` or a fork-controlled
  checkout in the same workflow.
- `actions: write`, `packages: write`, or `security-events: write` granted
  to a job that does not publish, push, or upload a scan result.

## Do NOT flag

- `permissions: {}` (no scopes), `permissions: read-all`, or an explicit
  read-only block at the workflow level with narrower job-level scopes.
- A write scope a documented step needs: `contents: write` for a release
  upload, `pull-requests: write` for a comment bot, `id-token: write` for a
  cloud OIDC exchange, `security-events: write` for a SARIF upload.
- A missing `permissions:` block on a `push`-only workflow with no secret
  and no write step, when the repository's default is already read-only.
  The diff cannot prove the default; do not claim a vulnerability it cannot
  show.
- An org/repo policy or a reusable workflow that sets permissions
  centrally, where the change under review does not widen them.
- Test workflows that run only on `workflow_dispatch` by maintainers with no
  secrets.

## Evidence

Quote the `permissions:` block (or its absence) and the trigger. Name the
scope and the action a compromised step could take with it.
