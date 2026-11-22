# Resource release and timers

Every acquired resource needs a release on every return path, installed at the
acquisition site. This rule also covers the two Go lifecycle changes that
decide whether a timer is a leak: Go 1.23 made unreferenced timers
garbage-collectable, and before that an un-stopped `Ticker` lived until
process exit.

## When to flag

- An `http.Response.Body` not closed on every path, including a non-2xx
  branch that returns early. Close after reading (and drain with
  `io.Copy(io.Discard, resp.Body)` when the connection should be reused).
- `sql.Rows` whose `rows.Err()` is never checked after the loop, or an early
  `return` inside the loop without `rows.Close()`. The implicit close at
  normal exhaustion does not report iteration errors; check `Err` every time.
- `os.Open`/`os.Create`, `net.Conn`, `gzip.Reader`, `zip.Reader`, a
  `*sql.Tx`, or a temporary file with no `Close`/`Rollback` on the error
  path, or a cleanup installed after the first operation that can fail.
- A `defer` inside a loop that can run an unbounded number of times, holding
  the handles, transaction, or lock until the enclosing function returns.
- A `time.NewTicker` created in a loop or request path and never stopped,
  when `go.mod` declares a language version below Go 1.23: before Go 1.23 an
  unreferenced ticker was not garbage-collected and its runtime timer leaked
  until process exit. On `go 1.23` or later, not stopping is still a
  dropped-tick/correctness question, but not the same unbounded leak.
- `time.After` inside a `select` loop on a module below Go 1.23: every
  iteration allocates a timer that stays live until it fires even when the
  other case wins. On Go 1.23+ the unreferenced timer is collectable, so do
  not report a leak there.
- `time.Tick` in code that can run repeatedly: its documentation has always
  warned that it leaks; Go 1.23 lets the garbage collector reclaim it, but a
  ticker the caller can no longer stop still cannot be shut down promptly.

## Do NOT flag

- `defer resp.Body.Close()` or `defer rows.Close()` installed immediately
  after the error check — the correct shape.
- `os.ReadFile`/`os.WriteFile` with no explicit close: those helpers own the
  descriptor.
- A `defer` in a loop bounded by a small compile-time constant (three header
  fields, two passes) where holding the resources until return is the
  intended scope.
- A `defer` inside a closure invoked once per iteration (`func() { defer
f.Close() }()`): the call returns each time, so the cleanup runs per
  iteration.
- An un-stopped `Ticker` in a `go 1.23`+ module where the ticker is dropped
  after the function returns and no `Stop` is needed for correctness, unless
  the program sets `GODEBUG=asynctimerchan=1`, which restores the old
  non-collectable timer behavior. Report the missed `Stop` only when the
  diff also depends on prompt shutdown.
- `time.After` in a one-shot `select` at function scope on Go 1.23+: one
  timer, collected once unreachable.

## Evidence

Quote the acquisition and the return path that skips release, or the timer
construction and the loop. Read `go.mod` before reporting a timer leak and
state the directive in the finding.
