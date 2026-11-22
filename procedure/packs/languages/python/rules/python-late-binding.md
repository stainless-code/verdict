# Late-binding closures in loops

A closure captures the variable, not its value at definition time. Functions
created in a loop therefore all see the loop variable's final value. This is
still true in Python 3.12: PEP 709 inlined comprehensions for speed but did
not change how the loop variable is bound.

## When to flag

- `funcs = [lambda: i for i in range(3)]`, or a `def` inside a `for` body
  that returns or stores a closure reading `i`; every call sees the last
  value.
- Callbacks registered in a loop that close over the loop variable
  (`for room in rooms: bus.subscribe(lambda msg: handle(room, msg))`).
- A closure over a variable that the loop reassigns in its body (`x = i`
  inside the loop does not help: `x` is one function-scoped cell).
- Deferred work queued in a loop (`loop.call_soon(lambda: use(item))`,
  `Thread(target=lambda: work(item))`) where the call runs after the loop.
- A closure over an `except ... as err` variable, which Python deletes at the
  end of the handler.

## Do NOT flag

- A closure invoked immediately within the iteration (an IIFE pattern) or a
  comprehension whose result is consumed before the next iteration.
- A value bound by a default argument (`lambda i=i: i`), by
  `functools.partial(handler, item)`, or by a factory function that takes the
  value as a parameter.
- A closure over a module constant or a loop-invariant value that does not
  change.
- A generator expression consumed immediately (`sum(f(x) for x in items)`).
- A class body or comprehension where the binding is intentionally the final
  value and the code documents it.

## Evidence

Quote the loop, the closure, and the call site that runs after the loop.
Name the value every closure sees instead of its iteration's value.
