Report a file, socket, subprocess pipe, or database connection opened without
`with` or a `finally` release, a lock acquired without release on the error
path, and a session created per request that is never closed.
