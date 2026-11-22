# Unbounded reads on a request path

A query with no bound returns whatever the table grows to. On a hot request
path that turns a normal table growth curve into latency, memory pressure,
and a timeout; `SELECT *` also ships columns the caller never reads.

## When to flag

- A `SELECT` on a request-handler or page-rendering path with no `LIMIT`,
  keyset pagination, or selective `WHERE`, over a table the diff shows
  growing (events, audit rows, messages).
- `SELECT *` from a wide table in a hot path where the caller uses a few
  columns.
- An ORM/repository call whose generated query has no limit on a
  collection endpoint, or a nested `N+1` load that multiplies an unbounded
  parent set.
- A loop that runs one unbounded query per item (a classic N+1) on a request
  path.
- `OFFSET` pagination over an unbounded table without a stable order, which
  both scans and can skip/duplicate rows.

## Do NOT flag

- `COUNT(*)`, `EXISTS (SELECT 1 ...)`, or an aggregate that returns one row.
- A query with a primary-key/unique predicate, a selective indexed `WHERE`,
  or an explicit `LIMIT`.
- A query against a small configuration/lookup table (a fixed enum table, a
  feature-flag table with tens of rows).
- A migration, analytics job, export, or admin script that is not on the
  request path; state the context before flagging.
- A `SELECT` whose result set is bounded by the schema (a `CHECK`
  constraint or a small enum) and whose size the diff shows.
- A query the caller streams and stops early (`LIMIT` applied by the driver
  or an iterator with a bound).

## Evidence

Quote the query and the request path that calls it. Name the table whose
growth is unbounded and the resource (latency, memory) the caller spends.
