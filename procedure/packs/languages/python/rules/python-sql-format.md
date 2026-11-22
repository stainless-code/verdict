# SQL built from formatted strings

A query assembled with an f-string, `%`, `.format`, or concatenation is not
parameterized. A quote in the value changes the statement; an interpolated
identifier can change the table or column. Drivers parameterize values, not
SQL text.

## When to flag

- `cursor.execute(f"SELECT ... WHERE id = {user_id}")`, `%`/`.format`
  formatting, or string concatenation with request data, headers, or a
  tenant-supplied value.
- An identifier (table, column, sort direction, schema) interpolated from
  input; placeholders cannot bind identifiers. Use a driver's identifier
  quoting (`psycopg2.sql.Identifier`, SQLAlchemy's quoted names) and an
  allowlist.
- A dynamic `WHERE` built by joining user-provided fragments, even when each
  value is quoted by hand.
- `executemany` with a formatted statement instead of a parameter tuple per
  row.
- A raw SQL string passed to an ORM's `text()`/`execute()` with values
  interpolated rather than bound.

## Do NOT flag

- `cursor.execute("SELECT ... WHERE id = %s", (user_id,))` and the
  driver-equivalent placeholders (`?` for sqlite3, `:name` for SQLAlchemy,
  `$1` for asyncpg). Placeholder style is not the defect.
- An ORM query DSL call whose arguments are bound, or a query builder that
  escapes identifiers from an allowlist.
- A constant query with no external input, even if it contains a literal.
- Table/column names selected by a closed `if`/mapping over known constants,
  with no interpolated user value.
- SQL that formats only a numeric value already validated as an integer and
  never reaches a string context; still prefer a bind parameter, but do not
  report it as injection without a path from untrusted input.

## Evidence

Quote the formatted statement and the input path. Name the value that
changes the SQL text and what the attacker can read or write.
