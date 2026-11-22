Report a `Closeable` — stream, reader, connection, statement, result set —
opened outside `try`-with-resources and not closed on every return path,
including the exception path that returns early.
