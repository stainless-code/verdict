# Defer inside unbounded loops

`defer` runs when the enclosing function returns, not when the iteration
ends. In a loop that can run many times, defers accumulate the resources,
locks, or transactions they release.

## When to flag

- A `defer` inside a loop whose trip count is chosen by input, a page
  cursor, a queue, or a retry policy: one deferred close per iteration, all
  running at function return. The process can exhaust file descriptors or
  hold a database transaction open for the whole loop.
- A deferred `Unlock` inside a loop: the mutex stays locked for the rest of
  the function, so the loop serialises itself or deadlocks on the next
  iteration.
- A deferred `Rollback` or `Commit` inside a loop over transaction work: the
  transaction lifetime is not the iteration's.
- A deferred cleanup in a long-lived worker's request loop, where the
  function returns only when the worker stops.

## Do NOT flag

- A `defer` in a loop with a small compile-time bound (two or three fixed
  resources) where holding all of them until return is the intended scope.
- A `defer` inside a closure called per iteration, because the closure
  returns each time and the cleanup runs then.
- A `defer` that releases something trivially cheap and bounded, such as
  `defer wg.Done()` in a loop that spawns a known small number of goroutines,
  when the loop cannot grow with input.
- A `defer` inside a per-request handler that returns after one request,
  even when a `select` loop outside it accepts the requests: the enclosing
  function is not the loop.

## Evidence

Quote the loop head and the `defer`. State how the trip count is bounded and
what stays open until return; an unbounded count is the finding.
