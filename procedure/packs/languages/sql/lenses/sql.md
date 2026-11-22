# SQL lens

Dispatch only when the diff changes schema, data, locking, or SQL text built
from input. Frame one falsifiable question — what is irreversible here, what
locks how much for how long, which value is interpolated, which statement
touches an unbounded row set — and give it a short hypothesis label.

Read the changed migration and the statements around it; the on-disk diff is
authoritative scope. Name the engine and version when it decides the answer:
PostgreSQL 11+ adds a column with a non-volatile default without a table
rewrite, `CREATE INDEX CONCURRENTLY` cannot run inside a transaction, and
MySQL autocommits DDL. Scope the question to at most ~12 files, cite NEW line
numbers, and name the table and the failure mode.
