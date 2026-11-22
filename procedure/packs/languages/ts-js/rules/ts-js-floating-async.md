# Floating promises and partial async failure

A promise that nothing awaits or catches turns a rejected operation into an
unhandled rejection. Node 15 and later terminate the process on an unhandled
rejection by default; browser and edge runtimes report it to the console and
leave the operation's resources unreleased.

## When to flag

- An async call whose promise is neither returned nor awaited: `fetch(url);
save(row);`, a `void`-ed call that can reject, or `.then(...)` with no
  `.catch(...)`.
- An `async` event, route, or callback handler whose rejected promise the
  host does not observe. Express 4 does not catch a rejected handler promise;
  Express 5 does, so state the framework and major version before flagging.
- `await` missing inside the `try` that was meant to catch it: the call is
  started inside the block but the rejection surfaces after the block ends.
- `Promise.all` on independent operations where one rejection leaves the
  others running and their side effects unrolled back, with no
  `AbortController` to cancel them. `Promise.allSettled` or a per-operation
  catch is the fix when every result must be accounted for.
- A `.catch` that logs and lets the caller continue as if the operation
  succeeded, or a retry loop whose final rejection is discarded.
- A fire-and-forget promise stored on an object and never awaited, or one
  whose rejection handler rethrows into an unobserved promise.

## Do NOT flag

- A deliberate fire-and-forget with an attached rejection handler
  (`.catch(handle)`), or a promise stored for a later `await` on every path.
- Top-level `await` in an ES module, and an async function whose returned
  promise the caller awaits.
- `void` used on a promise that cannot reject (a resolved literal) or on a
  call whose rejection the surrounding code handles elsewhere in the same
  diff.
- An `async` Express 5 handler, whose rejected promise the framework forwards
  to error middleware. Express 4 is the version that needs the finding.
- A promise settled by `Promise.allSettled` where the code inspects every
  result.

## Evidence

Quote the floating call and the path that continues after it. For a
framework handler, name the framework and version that decides whether the
rejection is observable.
