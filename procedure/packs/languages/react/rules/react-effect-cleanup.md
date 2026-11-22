# Effects that do not release what they start

`useEffect` runs after render and its cleanup runs before the next effect or
unmount. Without a cleanup, a subscription, timer, observer, socket, or
in-flight request survives the component and can still run its callback,
racing a newer mount.

## When to flag

- `useEffect` that subscribes with `addEventListener`, `.on`, `.subscribe`,
  a WebSocket, or an `EventSource` and returns no cleanup that removes the
  same listener or closes the same connection.
- `setInterval`/`setTimeout` started in an effect with no `clearInterval`/
  `clearTimeout` cleanup. Report an interval even when the component is
  long-lived: it can also outlive a route change.
- `fetch` in an effect with no `AbortController` and no cancellation path,
  so an old response can overwrite a newer one. React ignores the state
  update after unmount, but the network work and the race remain.
- `IntersectionObserver`, `ResizeObserver`, `MutationObserver`, or
  `requestAnimationFrame` started in an effect without `disconnect`/
  `cancelAnimationFrame`.
- A ref callback that attaches something and returns no cleanup, in a project
  whose `package.json` installs React 19 or later (React 19 supports ref
  cleanup functions; React 18 ignores a returned cleanup, so do not require
  one there).
- An external store subscription in an effect instead of
  `useSyncExternalStore`, when the diff changes how a shared store is read.

## Do NOT flag

- An effect with no resource: pure computation, a `document.title`
  assignment, or a one-time analytics event. There is nothing to release.
- An effect that returns its cleanup correctly, including a fetch effect
  that aborts in the cleanup. Do not ask for a second teardown.
- React 18 StrictMode's development double-mount. It makes a missing cleanup
  visible; it is not itself the defect, and it does not double-run production
  effects.
- A process-lifetime listener registered once by the root application shell
  that never unmounts (a theme or locale listener on the document).
- A subscription owned by a framework or library whose documented teardown
  the component never controls.
- A React 18 ref callback with no cleanup: cleanup functions are a React 19
  feature, so the absence is not a defect on 18.

## Evidence

Quote the effect and the resource it starts, then name what outlives the
component or which later response wins. State the React major version when
ref cleanup is part of the finding.
