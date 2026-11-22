# Conditional hooks and changing hook order

React identifies hooks by call order. A hook that runs on some renders and not
others changes every later hook's slot, producing "Rendered more hooks than
during the previous render" or state attached to the wrong hook.

## When to flag

- A hook called inside an `if`, `else`, `switch`, `for`/`while`, `&&`, or
  ternary expression in a component or custom hook.
- A hook called after a conditional early return (`if (!open) return null;`
  before `useState`/`useEffect`), so the count changes when the condition
  changes.
- A hook called inside a `try`/`catch` or a callback passed to another hook,
  where it runs conditionally or more than once per render.
- A custom hook that calls hooks conditionally internally, even when its own
  call site looks unconditional.
- A hook call moved into a helper whose name does not start with `use` and
  which is called conditionally.

## Do NOT flag

- Every hook called before an unconditional return that follows all hooks.
  An early return after the last hook is legal.
- `use()` in React 19: the `use` API may be called conditionally and in
  loops by design. Do not apply the Rules of Hooks to it as if it were
  `useState`.
- A component with no hooks, or a plain function with no hooks, regardless of
  its branches.
- A custom hook named `useSomething` that calls hooks unconditionally in its
  body, even if its caller is itself conditional as a unit.
- A lint warning from `eslint-plugin-react-hooks` that CI already gates:
  report the branch the diff changes, not a restatement of the plugin's
  message.

## Evidence

Quote the branch and the hook call. Name the render where the hook count or
order differs, and the state that would attach to the wrong slot.
