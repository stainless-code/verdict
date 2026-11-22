# Go lens

Dispatch only when the diff changes Go code that owns a lifecycle: a
goroutine, a context, a lock, a resource, or an error path that decides what
the caller sees. Frame one falsifiable question about that ownership and give
it a short hypothesis label.

Read the changed files the dispatch names; the on-disk diff is authoritative
scope. Scope the question to at most ~12 files, cite NEW line numbers, and
name the concrete failure — a goroutine that outlives its caller, a leaked
file descriptor, a data race — never "review the code".
