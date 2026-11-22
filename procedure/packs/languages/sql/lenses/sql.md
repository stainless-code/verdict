# SQL lens

Dispatch only when the diff changes schema, data, or a locking statement.
Frame one falsifiable question — what is irreversible here, what locks how
much for how long, which value is interpolated — and give it a short
hypothesis label.

Read the changed migration and the statements around it; the on-disk diff is
authoritative scope. Scope the question to at most ~12 files, cite NEW line
numbers, and name the table and the failure mode.
