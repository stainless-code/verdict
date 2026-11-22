# Lost, swallowed, or unwrappable errors

Report an error the caller needs but cannot see. Go gives the caller exactly
one thing to inspect: the returned error value. Losing it, flattening its
chain, or matching its text turns a recoverable failure into an opaque one.

## When to flag

- An error discarded with `_` (or not assigned) on a path whose caller must
  branch on the failure: a failed write, `json.Unmarshal`, `rows.Scan`, a
  network call, or a security check.
- An error logged and then dropped on a path that continues and later returns
  success. Logging is not handling.
- `fmt.Errorf("...: %v", err)` (or string concatenation) where a caller must
  use `errors.Is`/`errors.As`. `%v` formats the message and severs the chain;
  use `%w`. Multiple `%w` verbs are supported from Go 1.20, and
  `errors.Join` (Go 1.20+) is the tool for independent failures.
- A comparison by message text (`err.Error() == "..."`, `strings.Contains`)
  instead of `errors.Is`/`errors.As`. Message text is not a contract.
- A typed-nil error stored in an `error` interface: `var p *MyError; return p`
  makes `err != nil` true even though the pointer is nil.
- `panic` used for an ordinary request-time failure that a caller could
  handle, or `recover` that swallows the panic and returns success.

## Do NOT flag

- `_ = resp.Body.Close()` after the body was consumed, or `_ = f.Close()` on
  a read-only file: a close error there is usually not actionable. Flag a
  discarded close only when the write itself can fail and the caller reports
  success anyway.
- A boundary that deliberately seals an error with `%v` and documents or
  tests that decision (this repository does it at verb boundaries): the
  inspection contract is intentionally closed, not broken.
- `%w` on an error whose chain the package does not want to expose. Wrapping
  is a choice; report only when the caller is shown to inspect the cause.
- A test that compares `err.Error()` for a fixture's own exact output.
- `errors.New`/`fmt.Errorf` without `%w` when the error is newly created and
  has no cause to preserve.

## Evidence

Quote the discard or the `%v` wrap and the caller that needs the cause. State
what recovery or reporting is lost, not simply that an error exists.
