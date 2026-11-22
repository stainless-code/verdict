# Go lens

Dispatch only when the diff changes Go code that owns a lifecycle: a
goroutine, a context, a lock, a resource, an error path, or a slice another
caller still reads. Frame one falsifiable question about that ownership and
give it a short hypothesis label (`cancel-leak`, `rows-err-unchecked`).

Read the changed files the dispatch names and `go.mod`: the module's `go`
directive decides whether a loop-variable capture is a bug (Go 1.22 gave each
iteration its own variable) and whether an unreferenced `time.Timer` is
garbage-collected (Go 1.23). Scope the question to at most ~12 files, cite
NEW line numbers, and name the concrete failure — a goroutine that outlives
its caller, a leaked file descriptor, a data race — never "review the code".
