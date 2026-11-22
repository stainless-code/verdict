Report DDL that rewrites or exclusively locks a large table in one statement:
`ALTER TABLE ... ADD COLUMN ... NOT NULL DEFAULT`, a column type change, or an
index built without `CONCURRENTLY`, on a table the change also writes.
