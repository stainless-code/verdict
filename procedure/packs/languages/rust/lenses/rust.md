# Rust lens

Dispatch only when the diff changes a failure boundary, a lock's lifetime, or
an `unsafe` invariant. Frame one falsifiable question — which input makes this
panic, which guard is alive across an await, which invariant does this block
assume — and give it a short hypothesis label.

Read the changed files the dispatch names; the on-disk diff is authoritative
scope. Scope the question to at most ~12 files, cite NEW line numbers, and
state the failing input or interleaving.
