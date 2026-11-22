# Migrations that lock or rewrite a live table

A schema change takes a lock and sometimes rewrites every row. On a large
table the difference between a metadata-only change and a rewrite is the
difference between a sub-second deploy and an outage.

## When to flag

- PostgreSQL `CREATE INDEX` without `CONCURRENTLY` on a table that takes
  writes: a plain build holds a `SHARE` lock that blocks writes for the whole
  build. `CONCURRENTLY` cannot run inside a transaction block, so the
  migration tool must disable its wrapping transaction.
- `ALTER TABLE ... ADD COLUMN ... NOT NULL DEFAULT <volatile>` where the
  default is volatile (`random()`, `clock_timestamp()`, `gen_random_uuid()`):
  PostgreSQL must rewrite every row. A non-volatile default is metadata-only
  on PostgreSQL 11+ (see Do NOT flag).
- A column type change, a `VARCHAR` length shrink, or an `ALTER COLUMN`
  expression with `USING` that rewrites the table.
- `ALTER TABLE ... ADD CONSTRAINT ... CHECK (...)` without `NOT VALID`, or a
  foreign key added without `NOT VALID`, which scans and locks the table.
- `SET NOT NULL` on a large column with no validated `IS NOT NULL` check.
  PostgreSQL 12+ can skip the scan when a validated not-null `CHECK`
  constraint exists; do not assume the optimization without it.
- `VACUUM FULL`, `CLUSTER`, a long `LOCK TABLE`, or a `REINDEX` on a live
  table.
- MySQL DDL without an online/instant algorithm where the engine supports
  one (`ALGORITHM=INSTANT`/`INPLACE`, `LOCK=NONE`), or SQL Server index
  creation without `ONLINE = ON`.

## Do NOT flag

- PostgreSQL 11+ `ALTER TABLE ... ADD COLUMN ... DEFAULT <non-volatile>`:
  adding a column with a non-volatile default (`0`, `false`, `'x'`, `now()`,
  which is stable) is a metadata-only change and does not rewrite the table.
  A `NOT NULL` column with such a default follows the same fast path.
- Adding a nullable column with no default: metadata-only on PostgreSQL.
- `CREATE INDEX CONCURRENTLY` (with the transaction caveat handled) and
  `DROP INDEX CONCURRENTLY`.
- `ADD CONSTRAINT ... NOT VALID` followed by a separate validation step;
  validation takes a weaker lock than the original add.
- A migration against a table created in the same migration, a temporary
  table, or a table documented as small (a lookup table with tens of rows).
- A backfill written as batched `UPDATE ... WHERE id BETWEEN ...` statements
  that commit between batches.

## Evidence

Quote the statement and name the table and engine version. State whether the
operation rewrites rows or takes a lock that blocks writes, and for how long.
