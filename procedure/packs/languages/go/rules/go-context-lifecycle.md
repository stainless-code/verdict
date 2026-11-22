# Context and goroutine lifecycle

Trace every goroutine and `context.Context` the change adds. Each goroutine
needs an exit path its caller can trigger, and each derived context needs its
`cancel` called on every return path.

## When to flag

- A `context.WithCancel`, `WithTimeout`, or `WithDeadline` whose `cancel` is
  not called on every return path, including the error path. The leak keeps
  the parent's child list and the timer alive until the parent is canceled.
- `defer cancel()` inside a loop or a long-lived helper that runs for the
  process lifetime: the defers accumulate until the enclosing function
  returns, not until the iteration ends.
- A goroutine with no exit path the caller can trigger: it blocks on a bare
  channel receive, a `Ticker`, or a retry loop that never reads `ctx.Done()`.
  Report the spawn site and the missing `<-ctx.Done()` case.
- A child context derived from a request context and stored for use after the
  request returns, or an `http.Request` whose context is replaced with
  `context.Background()` so client cancellation is lost.
- `ctx` stored in a struct that outlives the call, or a context field set once
  and reused for unrelated requests. Pass the caller's context as the first
  parameter instead (Go 1.7+ convention; `containedctx` and `contextcheck`
  cover part of this).
- A method that accepts a context but spawns work with `context.TODO()` or
  `context.Background()` while the caller's context was cancellable.

## Do NOT flag

- `context.WithValue` on its own: it returns no cancel function, so there is
  nothing to release.
- `context.TODO()` used as an explicit placeholder in code the diff does not
  make concurrent, or in a test helper with no cancellation story.
- A `defer cancel()` in a function that returns promptly, even when the
  derived context is only used for one call inside that function.
- A goroutine whose exit is owned elsewhere and visible in the diff: a
  `Ticker` loop reading `ctx.Done()`, an `errgroup` member, or a worker whose
  stop channel the caller closes on its own return path.
- A deliberately detached context for work that must finish after the request
  (`context.WithoutCancel`, Go 1.21+) when the change documents why the
  request's cancellation must not stop it. Do not flag the detachment alone;
  flag a detached context that then outlives its owner with no timeout.

## Evidence

Quote the `WithCancel`/`WithTimeout` call and the return path that skips
`cancel`, or the goroutine body and the statement that never observes
`ctx.Done()`. Name the owning request or parent context.
