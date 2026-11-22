# Rust lens

Dispatch only when the diff changes a failure boundary, a lock's lifetime, an
`unsafe` invariant, a blocking call on an async executor, or an integer cast
that narrows a length or index. Frame one falsifiable question — which input
makes this panic, which guard is alive across an await, which invariant does
this block assume, which value truncates — and give it a short hypothesis
label.

Read the changed files and the crate's runtime setup (`#[tokio::main]`,
current-thread vs multi-thread, `spawn_blocking` use); the on-disk diff is
authoritative scope. Scope the question to at most ~12 files, cite NEW line
numbers, and state the failing input or interleaving.
