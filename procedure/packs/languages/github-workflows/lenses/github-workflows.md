# GitHub workflow lens

Dispatch only when the diff changes a workflow's trigger, permissions, a
checkout of repository or PR content, a shell step that interpolates an
expression, or an action's pin. Frame one falsifiable question — who can
trigger this, what executes, which secret or token is in scope — and give it
a short hypothesis label.

Read the changed workflow and the actions it calls; the on-disk diff is
authoritative scope. Distinguish `pull_request` (fork code runs without base
secrets and with a read-only token) from `pull_request_target`/`workflow_run`
(base context, secrets and write token available). Scope the question to at
most ~12 files, cite NEW line numbers, and name the trigger and the
privilege.
