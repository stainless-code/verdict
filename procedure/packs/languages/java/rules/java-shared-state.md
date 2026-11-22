# Shared mutable state and unsafe publication

A field reachable from more than one thread needs a happens-before edge.
Without one, a reader can see a stale value, a half-constructed object, or a
corrupted collection.

## When to flag

- A shared `SimpleDateFormat` or `Calendar` instance: both are documented as
  not thread-safe. Use `java.time.format.DateTimeFormatter`, which is
  immutable and thread-safe.
- A `HashMap`/`ArrayList`/`StringBuilder` stored in a static field or a
  singleton and mutated after publication without synchronization. Use
  `ConcurrentHashMap`, a synchronized wrapper, or an immutable copy.
- Double-checked locking without `volatile` on the published field, or a
  lazily initialized singleton whose reference is not `volatile`/final. The
  Java Memory Model (Java 5+) does not make the unsynchronized read safe
  otherwise.
- A non-atomic check-then-act on shared state (`containsKey` followed by
  `put`) that two threads can interleave.
- A field written by a request thread and read by a background worker with no
  `volatile`, atomic, lock, or queue handoff.
- A mutable `static` collection exposed directly (returned or made
  accessible) to callers who mutate it.

## Do NOT flag

- `ConcurrentHashMap`, `AtomicInteger`/`AtomicReference`, `synchronized`
  blocks, `ReentrantLock`, or an immutable object (`String`, `java.time`
  types, a record of immutable fields).
- A field confined to one thread: a local, a method parameter, or a value
  stored in a `ThreadLocal` and never shared.
- A `final` field assigned in the constructor and never mutated, including a
  `final` reference to a mutable object the class does not mutate after
  construction.
- A collection built once at startup and published through a static final
  field, then only read (prefer `List.copyOf`/`Map.copyOf` to make it
  explicit).
- Test code with no second thread.

## Evidence

Quote the shared field and the write/read pair. Name the two threads and the
stale or torn value a reader sees.
