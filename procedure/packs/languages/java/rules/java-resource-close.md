# Closeable resources outside try-with-resources

A stream, reader, connection, statement, or result set owns an OS or server
resource. `try`-with-resources (Java 7+) closes it on every path and attaches
suppressed exceptions; a manual `close()` in the happy path does not.

## When to flag

- An `AutoCloseable` acquired outside `try`-with-resources and not closed on
  every branch, including the exception path that returns early.
- A `Connection`/`Statement`/`ResultSet`/`InputStream`/`Reader`/`Channel`
  opened in a helper and returned without transferring an explicit ownership
  contract.
- A manual `finally { resource.close(); }` whose own exception masks the
  primary failure, or that throws from `finally`.
- A `Files.newInputStream`/`FileChannel.open`/socket accepted in a loop and
  closed only after the loop.
- A resource variable reassigned inside the `try` (Java 9+ only allows
  effectively final resource variables), forcing a manual close path.

## Do NOT flag

- A resource declared in `try (...)`; that is the fix, including Java 9+
  effectively final variables.
- An in-memory stream with a no-op close (`ByteArrayInputStream`,
  `StringWriter`, `ByteArrayOutputStream`) that holds no OS handle.
- `Files.readString`/`readAllBytes`/`readAllLines`, which close internally.
- A resource whose lifecycle a framework owns and whose close is documented
  as the container's job (a Spring-managed `DataSource` connection returned
  to its pool, a servlet request stream).
- A resource closed in a `finally` that correctly preserves the primary
  exception and does not swallow it; prefer try-with-resources, but the leak
  is not there.

## Evidence

Quote the acquisition and the return path that skips close. Name the handle
or connection that stays open.
