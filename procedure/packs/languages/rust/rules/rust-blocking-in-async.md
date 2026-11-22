# Blocking work on an async executor

An async task is polled by an executor thread. A blocking call parks that
thread, so every task scheduled on it waits — including timers and
cancellation. Dropping a `JoinHandle` hides the task's failure rather than
canceling it.

## When to flag

- `std::thread::sleep`, `std::fs` I/O, a blocking `TcpStream`, or a
  synchronous database/HTTP client inside an `async fn`, without
  `tokio::task::spawn_blocking` (or the runtime's equivalent).
- `reqwest::blocking`, `ureq`, or a sync SDK called from an async handler.
- A long CPU-bound loop inside `async fn` (hashing, compression, parsing a
  large payload) that does not offload to `spawn_blocking`.
- A `tokio::spawn`/`spawn_blocking` `JoinHandle` dropped immediately
  (`tokio::spawn(work());` or `let _ = tokio::spawn(work());`) so a panic or
  error is never observed. Dropping the handle detaches the task; it does
  not cancel it.
- A detached task whose errors are the only signal of failure and are never
  joined or instrumented.

## Do NOT flag

- Work already inside `spawn_blocking`, `tokio::fs`, `tokio::time::sleep`,
  or an async client correctly awaited.
- Short CPU work (a few arithmetic operations, a small allocation) that does
  not hold the executor for a meaningful time.
- `std::thread::spawn` at a synchronous entry point outside the async
  runtime, with a join or a documented lifetime.
- A `JoinHandle` that is awaited or collected in a `JoinSet`; dropping a
  handle intentionally for a detached, self-contained task with its own
  error reporting (metrics, tracing) is not by itself a finding.
- A blocking call in a `#[cfg(test)]` test or a synchronous helper the async
  code never calls.

## Evidence

Quote the blocking call and the async context, or the dropped handle and the
error path it loses. Name the executor thread or failure that goes
unobserved.
