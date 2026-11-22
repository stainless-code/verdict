# Resources without deterministic release

Files, sockets, subprocess pipes, database connections, and locks need a
deterministic release on every path. CPython often closes a file when the last
reference dies, but that is a garbage-collection accident, not a contract, and
it does not cover sockets, transactions, or locks.

## When to flag

- `open(...)`, `socket.socket(...)`, `sqlite3.connect(...)`, or
  `subprocess.Popen(...)` assigned without `with` or a `try/finally` that
  closes it on the exception path.
- A `threading.Lock`/`asyncio.Lock` acquired without a `with`/`async with` or
  a `finally: release()`, especially around code that can raise.
- A database connection or session created per request and never closed or
  returned to its pool.
- A file opened inside a generator and never closed when the generator is
  abandoned.
- An `aiohttp.ClientSession` or `httpx.Client` created per call without
  `async with`/`with`; the connection pool and sockets stay open.

## Do NOT flag

- `with open(...)`, `async with session`, `contextlib.closing(...)`, or a
  `finally: handle.close()` that covers the error path.
- `Path.read_text()`/`write_text()`, `json.load`/`json.dump` with a path
  helper that opens and closes inside the standard library.
- `subprocess.run(...)`, which waits and closes its pipes; only a `Popen`
  that the code never waits on or closes is a finding.
- An in-memory buffer such as `io.StringIO`/`BytesIO` that is released with
  its owner and holds no OS resource.
- A session or connection injected by a framework dependency (FastAPI
  `Depends`, Django's connection middleware) whose lifecycle the framework
  owns.

## Evidence

Quote the acquisition and the return path that skips release. Name the
descriptor, connection, or lock that stays held.
