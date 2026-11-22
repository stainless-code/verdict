# SQL text assembled from input

A query assembled by concatenation, `format`, or a dynamic `EXECUTE` treats
input as SQL syntax. Values must be bound; identifiers cannot be bound and
need quoting plus an allowlist.

## When to flag

- A statement built by concatenation, `format`, or string interpolation from
  request data, a tenant value, a header, or a queue message.
- A dynamic `EXECUTE`/`EXECUTE IMMEDIATE` in a stored procedure whose text
  includes input without `quote_ident`/`quote_literal` (PostgreSQL `format`
  with `%I`/`%L`) or a bind parameter.
- An identifier (table, column, schema, sort direction) interpolated from
  input; a bind parameter cannot stand in for an identifier, so an allowlist
  plus quoting is required.
- A dynamic `IN (...)` list built by joining string values instead of
  generating placeholders.
- An ORM's raw/`text()` query with values spliced into the string rather
  than bound.

## Do NOT flag

- Parameterized statements (`?`, `$1`, `:name`) with values passed
  separately.
- A constant SQL string with no external input, even when it is built with
  `format` from constants.
- Dynamic SQL whose only variable parts are chosen by a closed `CASE`/mapping
  over known identifiers, with no raw input.
- A query builder / ORM DSL that binds values or quotes allowlisted
  identifiers.
- A migration or admin script that formats only a numeric value already
  validated as an integer; prefer a bind, but do not call it injection
  without an untrusted path.

## Evidence

Quote the SQL construction and the input path. Name the value that changes
the statement and what the attacker can read or write.
