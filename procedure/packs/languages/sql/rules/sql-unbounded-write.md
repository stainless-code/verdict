# UPDATE or DELETE with no real predicate

A write without a predicate changes every row. A predicate that is always
true (`WHERE 1=1`, `WHERE true`, a tautology from a default filter) does the
same, and neither is recoverable after commit without a backup.

## When to flag

- `UPDATE table SET ...;` or `DELETE FROM table;` with no `WHERE` clause in a
  migration, data-fix script, or application query.
- A `WHERE` that is always true (`1=1`, `true`, `'a'='a'`) or is assembled
  from optional fragments that can reduce to nothing.
- A `DELETE` whose only predicate is on a non-selective column that the diff
  does not constrain (a status set for most rows), where the intended target
  was one row.
- A `TRUNCATE` used where a bounded `DELETE` was intended (see also
  `sql-destructive-ddl`).
- A MySQL `UPDATE`/`DELETE` with no `LIMIT` where the diff's intent is a
  single-row fix and no key predicate is present.

## Do NOT flag

- A statement with a primary-key or unique-key predicate, or a bounded range
  (`WHERE created_at < :cutoff`) that the migration intends.
- A batched backfill (`WHERE id BETWEEN :lo AND :hi` committed per batch), or
  a retention job bounded by time and run deliberately.
- `UPDATE ... WHERE false` used to no-op in a generated migration.
- A `DELETE` inside a transaction that the same migration rolls back on
  failure, when the predicate is still bounded.
- Test fixtures that intentionally clear a temporary table.

## Evidence

Quote the statement and its `WHERE` (or its absence). Name the rows that
change and why the predicate cannot select only the intended subset.
