# SQL text built from input

A JDBC `Statement` executes the string it is given. Concatenating or
formatting input into that string changes the statement; a `PreparedStatement`
only protects the values bound through `?`.

## When to flag

- `statement.executeQuery("... " + userInput)` / `executeUpdate` /
  `execute` on a `Statement`, or `String.format`/`StringBuilder` building the
  same SQL.
- A `PreparedStatement` whose SQL text still interpolates an identifier
  (table, column, sort direction, schema) from input while values are bound.
- JPA/Hibernate `createQuery`/`createNativeQuery` with concatenated JPQL/SQL,
  or a `CriteriaBuilder` `literal` used to splice input.
- A dynamic `IN (...)` list built by joining string values instead of
  generating bind placeholders.
- A stored procedure or function name selected from input and concatenated
  into the call.

## Do NOT flag

- `PreparedStatement` with `?`/named bind parameters, including a generated
  placeholder list for an `IN` clause.
- JPA Criteria API, Querydsl, jOOQ, or an ORM's parameter binding.
- A constant SQL string with no external value, or a statement whose only
  dynamic part is a numeric value already validated as an integer (still
  prefer a bind; do not report it as injection without an untrusted path).
- An identifier chosen from a closed allowlist mapping (`switch` over known
  column names), where no raw input reaches the SQL text.
- Test SQL and migration scripts that run as a trusted operator.

## Evidence

Quote the SQL construction and the input path. Name the value that changes
the statement and what the attacker can read or write.
