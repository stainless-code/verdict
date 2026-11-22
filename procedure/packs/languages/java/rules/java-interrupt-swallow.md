# Swallowed interruption

`InterruptedException` is the JVM's cancellation signal. Catching it and
continuing clears the thread's interrupt status, so the task, executor, or
shutdown path that asked the thread to stop never learns it was ignored.

## When to flag

- `catch (InterruptedException e) { ... }` whose body does not call
  `Thread.currentThread().interrupt()` and does not rethrow or exit the loop.
- A `catch (Exception e)` around `Thread.sleep`, `wait`, `join`, a blocking
  queue take, or an executor await that swallows the interruption.
- A retry loop that catches `InterruptedException` and sleeps/retries, so
  `shutdownNow` cannot stop it.
- A wrapper that converts `InterruptedException` to a generic runtime
  exception without restoring the flag.
- A `finally` or cleanup path that catches and ignores interruption while a
  task is being canceled.

## Do NOT flag

- A catch that restores the flag (`Thread.currentThread().interrupt()`) and
  then returns or rethrows.
- A catch that rethrows `InterruptedException` (or wraps it and restores the
  flag) so the caller can decide.
- A catch on a thread that is deliberately not interruptible and is about to
  exit the task anyway, with the exit visible in the diff.
- A library that wraps the exception in a documented cancellation type and
  restores the flag before throwing.
- Test code that interrupts a fixture thread and asserts the fixture's
  handling.

## Evidence

Quote the catch and the path after it. Name the cancellation request that is
lost and what keeps running.
