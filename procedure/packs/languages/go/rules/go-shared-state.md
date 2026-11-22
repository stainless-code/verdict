# Shared mutable state and loop-variable capture

Report state two goroutines can reach without a happens-before edge, and the
idioms that make the race easy to miss: a captured loop variable and a
`WaitGroup` incremented after its `go`.

## When to flag

- A loop variable is captured by a goroutine or a deferred call, and the
  module's `go.mod` declares `go 1.21` or lower. Go 1.22 changed `for` loops
  so each iteration has its own copy of the iteration variable; the shared
  variable semantics apply only below that language version. Read the `go`
  directive: `go 1.22` or later clears this finding, `go 1.21` or lower
  keeps it.
- A map written from more than one goroutine without a mutex or channel
  handoff. Concurrent map writes panic (`concurrent map writes`), not just
  race the detector.
- `sync.WaitGroup.Add` called after the `go` statement it is meant to cover,
  or inside the goroutine: `Wait` can return before the work is registered.
  Call `Add` in the same frame as `go` (the `waitgroup` vet analyzer catches
  the common cases).
- A struct field or package-level variable read by one goroutine while
  another writes it with no synchronization: a cache map, a shared counter,
  a lazily initialized value, or a `bytes.Buffer` handed to two goroutines.
- A slice or map captured by a goroutine and mutated after the `go` without
  a copy or handoff, even when the goroutine only reads it.
- A `sync.Once` replaced by a boolean guard, or a double-checked lock without
  an atomic or mutex on the read side.

## Do NOT flag

- A captured loop variable in a module whose `go.mod` declares `go 1.22` or
  newer. `for i := range n { go func() { use(i) }() }` is safe there; that is
  the language semantics, not a race.
- A value that is never mutated after publication: a configuration struct
  built before the goroutines start, a read-only map populated once, or a
  constant.
- Values passed as arguments to the goroutine (`go work(item)`) rather than
  captured, and channels that move ownership of a value to one receiver.
- A mutex, `sync.Map`, `atomic.Int64`, or a channel used correctly; report
  the unprotected access, not the presence of concurrency.
- Test code whose goroutines are joined before the test returns and whose
  shared state is only read by the test goroutine.

## Evidence

Quote the capture or the unsynchronized access and the second goroutine.
For loop variables, state the `go.mod` directive that decides the answer.
