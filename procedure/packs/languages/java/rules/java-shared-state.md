Report mutable state reachable from more than one thread without `synchronized`,
`volatile`, an atomic, or a concurrent collection: a static field, a shared
`HashMap` mutated after publication, or a check-then-act on a value another
thread writes.
