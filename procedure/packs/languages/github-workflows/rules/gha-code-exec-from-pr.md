# Untrusted PR code executing in a privileged workflow

`pull_request_target` and `workflow_run` run in the base repository's
context: secrets and a write-capable `GITHUB_TOKEN` are available. Checking
out or executing the pull request's head code there runs an attacker's code
with those privileges.

## When to flag

- `pull_request_target` combined with `actions/checkout` of the PR head: a
  `ref` or `repository` expression that resolves the contributor's commit,
  or any checkout that otherwise reaches head code.
- A step that runs repository code after such a checkout: `npm install`,
  `make`, `pytest`, a build script, or a `run:` that invokes a file from the
  checkout.
- `workflow_run` triggered by a fork-visible workflow and then checking out
  `github.event.workflow_run.head_sha`/`head_branch` and running it with
  secrets or a write token.
- `issue_comment` or `workflow_dispatch` handlers that check out a
  commenter-supplied ref or run a commenter-supplied command.
- A cache restore/save around PR code in a privileged workflow where the
  cache key can be poisoned for a later trusted run.

## Do NOT flag

- `pull_request` triggers: fork PRs run without repository secrets and with
  a read-only token by default (unless the maintainer enabled "send secrets
  to fork PRs", which the workflow cannot show). Do not flag the trigger
  alone.
- A `pull_request_target` workflow that only labels, comments, or reads
  metadata and never checks out or executes head code.
- `actions/checkout` of the base ref (the default behaviour of
  `pull_request_target`) with no path to head content.
- A privileged job behind an `environment:` with required reviewers, or a
  maintainer-applied label gate, where the diff shows the gate and no
  head-code execution before it.
- A `workflow_run` job that only downloads artifacts and publishes a
  report, with no execution of PR-authored code and no write scope.
- A job whose `permissions: {}`/read-only token and empty secret scope make
  the execution unprivileged; state that in the finding only if execution
  still happens.

## Evidence

Quote the trigger, the checkout `ref`/`repository`, and the step that runs
the code. Name the secret or write permission the attacker gains.
