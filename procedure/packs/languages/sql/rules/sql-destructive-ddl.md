# Irreversible DDL in a deploy

A migration that drops or truncates data in the same release that ships the
code leaves no application-level rollback. The old code may still be running
during the deploy, and the rows are gone before anyone notices.

## When to flag

- `DROP TABLE` on a table the application still reads or writes in the same
  release, without a deprecation window.
- `DROP COLUMN` on a populated column, even on engines where the metadata
  operation is fast (PostgreSQL): the data is unrecoverable.
- `TRUNCATE` on a live table. It is transactional in PostgreSQL but
  implicitly commits in MySQL, and it cannot be rolled back after the
  migration commits.
- `ALTER TABLE ... DROP CONSTRAINT` on a constraint that still protects
  data, or a destructive change with no backup/restore step in the migration
  plan.
- A `DELETE` that removes a table's full contents as part of a schema
  migration (see `sql-unbounded-write` for the predicate check).

## Do NOT flag

- `DROP TABLE IF EXISTS` / `DROP INDEX` on a temporary table or an index the
  same migration created and no longer needs.
- A down/rollback migration that reverses a paired up migration explicitly,
  when the pair is visible in the diff.
- `DROP COLUMN` on a column added earlier in the same migration and never
  populated (the migration is self-contained).
- A test or fixture schema dropped in a teardown script.
- A retention job's `DELETE` with a bounded predicate; that is routine data
  lifecycle, not destructive schema DDL.

## Evidence

Quote the destructive statement and the table. Name the rows or column lost
and why the deploy cannot restore them.
