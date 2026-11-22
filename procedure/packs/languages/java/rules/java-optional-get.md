# Optional.get() without a presence check

`Optional.get()` throws `NoSuchElementException` when the value is absent. On
a value produced by a lookup it turns a normal empty result into a 500.

## When to flag

- `optional.get()` with no preceding `isPresent()`/`isEmpty()` check on the
  same value, where the optional comes from a repository, map lookup, stream
  reduction, parse, or other fallible source.
- `optional.get()` after a check on a different optional, or after a check
  whose branch does not dominate the call.
- A chain `findFirst().get()` on a stream that can be empty.
- `Optional.get()` inside a lambda or loop whose emptiness is not guaranteed
  by the surrounding code.
- `orElseThrow()` with no argument on a Java version below 10 (use a supplier
  or a checked exception), or a `get()` used to avoid handling absence.

## Do NOT flag

- `orElse`, `orElseGet`, `orElseThrow`, `map`, `flatMap`, `ifPresent`,
  `ifPresentOrElse`, or a `switch`/pattern match that handles both states.
- A `get()` immediately dominated by `isPresent()` in the same block with no
  intervening reassignment.
- An `Optional.of(...)` value (never empty) passed directly to `get()` in the
  same expression.
- A `get()` in a test after an explicit presence assertion.
- A project that uses a null-safe wrapper type with its own accessor; this
  rule is about `java.util.Optional`.

## Evidence

Quote the `get()` and the source optional. Name the empty result that throws
and the request that fails.
