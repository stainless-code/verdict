Report an async call whose rejection nothing can observe: a promise neither
awaited nor returned, an `await` missing inside the `try` that was meant to
catch it, an async event or route handler with no catch, or a `void`-ed promise
that can reject. A `.catch` that only logs and lets the caller continue is the
same loss.
