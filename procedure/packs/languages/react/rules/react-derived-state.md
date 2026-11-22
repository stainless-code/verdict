# Derived state and cascading effect updates

State that can be computed from props or other state does not need its own
`useState` plus a synchronizing effect. The extra render pass can show a stale
frame and the effect chain becomes a second, invisible data flow.

## When to flag

- `useEffect(() => setFullName(first + " " + last), [first, last])`: compute
  the value during render instead.
- An effect that mirrors a prop into state (`useEffect(() => setValue(prop),
[prop])`) so the component renders once with the old prop, then again.
  Keying the component or deriving the value is the fix.
- An effect that sets state from another piece of state, creating a chain of
  effects where one update schedules the next.
- An effect that resets a form or draft when an id prop changes, instead of
  keying the subtree on that id.
- A `useEffect` whose entire body is one or more `setState` calls driven by
  values already available during render.

## Do NOT flag

- Setting state from an async result in an effect (`setData(await fetch...)`,
  `setLoading`, `setError`): that is an external boundary, not derived state.
- An effect that synchronizes with an external system (a DOM API, a
  subscription, an animation, a non-React store) and sets state as the
  bridge.
- `setState` in an event handler, a reducer, or a callback: that is the
  ordinary update path.
- A deliberate snapshot of a prop that must not track later prop changes (an
  editor's initial draft) with a comment and a reason; prefer a ref, but do
  not flag the intent by itself.
- The documented "adjust state while rendering" pattern for derived values
  that cannot be computed cheaply, where the code compares against a previous
  value and React re-renders immediately without committing the stale frame.
- A state update that only runs once at mount to initialize from a browser
  API unavailable during server rendering.

## Evidence

Quote the effect and the state it sets. Show the derived expression that
could be computed during render, or the extra render that shows the stale
value.
