# GitHub workflow lens

Dispatch only when the diff changes a workflow's trigger, permissions, or a
step that runs repository or PR-controlled content. Frame one falsifiable
question — who can trigger this, what executes, which secret or token is in
scope — and give it a short hypothesis label.

Read the changed workflow and the actions it calls; the on-disk diff is
authoritative scope. Scope the question to at most ~12 files, cite NEW line
numbers, and name the trigger and the privilege.
