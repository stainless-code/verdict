# Blocking work inside async code

An `async def` runs on the event loop. A synchronous call that waits on the
network, the disk, a database, or a sleep blocks every other task on that
loop, not just the current request.

## When to flag

- `time.sleep(...)` inside `async def`; use `await asyncio.sleep(...)`.
- A synchronous HTTP client inside `async def`: `requests`, `urllib`,
  `http.client`, or a sync SDK. Use `httpx.AsyncClient`, `aiohttp`, or move
  the call to `asyncio.to_thread`.
- A synchronous database driver or ORM call inside `async def`: `psycopg2`,
  `pymongo`, `sqlite3`, a sync SQLAlchemy `Session`. Use the async driver or
  an executor.
- `subprocess.run(...)`, `os.system(...)`, `socket.recv(...)`, or a blocking
  file read of a large file inside `async def`, without
  `asyncio.to_thread`/`run_in_executor`/`spawn_blocking` equivalent.
- A long CPU-bound loop (hashing a large payload, image work, parsing a huge
  document) inside `async def`; the loop holds the event loop just as a sleep
  does.
- A synchronous callback invoked by an async framework (an aiohttp handler
  calling `requests.get`).

## Do NOT flag

- `await asyncio.sleep(...)`, an async client awaited properly, or a call
  wrapped in `asyncio.to_thread` (Python 3.9+) or
  `loop.run_in_executor(...)`.
- Short CPU work that finishes in microseconds (formatting a small string,
  building a dict) with no I/O.
- A synchronous function called from synchronous code, even if an async
  function elsewhere calls it after offloading.
- `asyncio.run(...)` at a script or worker entry point, outside `async def`:
  that is the boundary that starts the loop.
- A framework that documents a sync handler as running in its own thread
  pool (Starlette's `def` endpoints, Django sync views).

## Evidence

Quote the `async def` and the blocking call inside it. Name the loop or
request that waits behind it.
