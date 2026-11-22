# Long-lived handler cleanup

A handler that outlives one request — a server, a worker, a CLI process, a
websocket connection — must release the timers, listeners, and subscriptions
it starts. This is the non-React half of cleanup; effects belong to the
`react` pack.

## When to flag

- `setInterval`/`setTimeout` started in a request, connection, or per-tenant
  path with no `clearInterval`/`clearTimeout` on the shutdown path. An
  interval also keeps a Node process alive unless `unref` is called.
- `addEventListener`/`on`/`subscribe` on a shared emitter, socket, or
  process signal in a per-request path, with no matching `removeEventListener`/
  `off`/`unsubscribe` when that request ends.
- A `fetch` or stream in a long-lived handler with no `AbortController`/
  `AbortSignal`, so a disconnected client leaves the upstream work running.
- A watcher (`fs.watch`, a queue consumer, a database LISTEN) created per
  reconnect without closing the previous one, so each reconnect adds a
  listener.
- A timer or listener that closes over a large per-request object: even a
  one-shot timer retains the whole closure until it fires.

## Do NOT flag

- A process-lifetime listener registered once at startup (a `SIGINT`
  handler, a single health-check interval) where no shutdown path exists
  yet.
- A one-shot `setTimeout` in a short-lived request handler that does not
  retain a large object; it fires once and releases the closure.
- An `AbortSignal.timeout(ms)` signal: it cleans itself up.
- A framework-owned subscription whose teardown the framework documents
  (an Express route, a Fastify hook, a worker pool task) and whose lifetime
  is the process's.
- A cleanup that exists elsewhere in the diff: a `finally` block, a
  `dispose()` method the caller invokes, or a `using`/`await using`
  declaration.
- React effects: report those under the `react` pack, not here.

## Evidence

Quote the registration and the shutdown path that does not release it. Name
what accumulates per request or reconnect.
