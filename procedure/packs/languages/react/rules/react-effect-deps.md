# Stale closures and missing effect dependencies

An effect closes over the render's props, state, and locals. Omitting a value
its dependency array does not freeze a safe snapshot: the effect keeps reading
the first render's value while a new render is on screen.

## When to flag

- An effect reads a prop, state value, or local derived from them, and the
  dependency array omits it, so the callback keeps the mount-time value. A
  `setInterval` that increments a captured `count` is the canonical failure:
  it writes `1` forever.
- An effect whose only dependency is `[]` but which reads a changing value
  and writes state or calls a side effect with it.
- A dependency array that lists an object or array rebuilt every render
  (`[{}]`, `[items.filter(...)]`, `[options ?? {}]`), which makes the effect
  run after every render and can cascade into a state loop.
- A cleanup that closes over a stale value and therefore releases the wrong
  resource (the second connection, not the first).
- A missing dependency hidden by an `exhaustive-deps` suppression comment
  where the captured value demonstrably changes.

## Do NOT flag

- `setState`/`dispatch` from `useState`/`useReducer`: React guarantees they
  are stable, so they do not belong in the dependency array.
- A ref object or `ref.current` read: the ref identity is stable and the
  current value is deliberately outside dependency tracking. Do not demand
  `ref` in the array as a way to fix a stale _value_; that is not a fix.
- A functional update (`setCount(c => c + 1)`) that avoids reading the
  captured value. The dependency is genuinely unnecessary.
- An effect whose captured value is a module constant, a prop documented and
  typed as immutable for the component's lifetime, or an `useEffectEvent`
  callback (React 19.2+, where installed) designed to read the latest values
  without dependencies.
- A mount-only effect with an explicit, commented decision that the
  mount-time value is the intended one, where the value is not expected to
  change (an initial theme read, a first-focus call).
- React Compiler's presence: it memoizes render output but does not change
  effect dependency semantics, so do not suppress a real stale closure
  because the compiler is enabled.
- A pure `useMemo`/`useCallback` dependency list with no effect: that is
  memoization, not a lifecycle bug.

## Evidence

Quote the effect, the value it reads, and the dependency array. Name the
stale value and the user-visible result after a re-render.
