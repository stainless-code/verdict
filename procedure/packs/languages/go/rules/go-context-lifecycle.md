Trace every goroutine and `context.Context` the change adds: each goroutine
needs an exit path its caller can trigger, and each `WithCancel`/`WithTimeout`
needs its cancel called on every return path. Report a goroutine that cannot
exit, a context stored in a struct, a `defer cancel()` inside a loop, or a child
context that outlives the request that created it.
