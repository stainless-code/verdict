# Mutex copies and locks held across blocking I/O

Report a lock whose value or lifetime is wrong: a copied mutex or a guard
held across work that can block for an unbounded time.

## When to flag

- A `sync.Mutex` or `sync.RWMutex` held across blocking work: a network
  call, a database query, a channel receive, a filesystem operation on a slow
  volume, or a callback into code the diff does not control. Every other
  goroutine waits behind it, including health checks.
- A lock acquired in a loop iteration and released only after the next
  iteration's work, or a `defer` that holds it until function return.
- A mutex copied after first use where `go vet` cannot see it: stored in an
  `any`, copied through a generic container, serialized, or copied by
  reflection. The copy duplicates the lock state and the two guards no longer
  exclude each other.
- `RWMutex.RLock` upgraded to `Lock` while the read lock is still held
  (self-deadlock), or lock-order inversion between two mutexes acquired in
  opposite orders on different paths.
- A `sync.Mutex` used to guard a value that is also read with `atomic` or
  written outside the lock, so the lock is not the only synchronization.

## Do NOT flag

- A mutex copy that `go vet`'s `copylocks` analyzer already rejects (a value
  receiver on a mutex-bearing type, a range variable copy). Do not restate
  the compiler's finding; report lock span, ordering, or escapes instead.
- A `sync.Mutex` zero value in a struct that is never copied, or a pointer to
  a mutex shared deliberately.
- A lock held for a few map operations, a counter increment, or an in-memory
  copy: held briefly is the point.
- A `sync.RWMutex` read lock held across a pure CPU computation with no
  blocking call.
- A single-goroutine program using a mutex for API shape or a future
  concurrency change; without a second goroutine there is no contention.

## Evidence

Quote the `Lock` and the blocking call inside its span, or the copy that
escapes the vet analyzer. Name the other goroutine that waits or the state
the copied guard fails to protect.
