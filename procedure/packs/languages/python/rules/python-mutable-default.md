# Mutable default arguments

Default values are evaluated once, when the `def` executes. A list, dict, set,
or other mutable object used as a default is therefore shared by every call,
and any mutation leaks into the next request.

## When to flag

- A default of `[]`, `{}`, `set()`, `bytearray()`, `collections.deque()`, or
  another mutable instance on a function or method that mutates it (append,
  update, add, pop) or returns it to a caller who can.
- `def handler(items=[])` where the body does `items.append(...)` or
  `items[key] = ...`; the second call sees the first call's data.
- A mutable default that is captured by a nested function and mutated later,
  even if the outer body only reads it.
- A default object whose identity is compared or cached across calls.
- A lambda or partial with a mutable default used as mutable state.

## Do NOT flag

- `None` as the default with `if value is None: value = []` in the body: that
  is the fix.
- An immutable default: `0`, `""`, `()`, `frozenset()`, `False`, or a
  module-level constant that the function never mutates.
- A mutable default the function only reads and never returns, where no
  caller can observe the shared identity.
- A `@dataclass` field using `field(default_factory=list)`, or a plain
  `field(default=[])`: dataclasses reject a `list`/`dict`/`set` default at
  class creation in every version since 3.7, and since 3.11 reject any
  unhashable default, so it cannot silently ship.
- A default that is a fresh object built at definition time for a
  deliberately process-wide cache, documented as such (report the sharing
  only if a caller can mutate it).

## Evidence

Quote the signature and the mutation. Name the data that leaks from one call
to the next.
