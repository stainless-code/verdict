# Bare except that swallows control-flow exceptions

`except:` catches `BaseException`, not `Exception`: `KeyboardInterrupt`,
`SystemExit`, and, since Python 3.8, `asyncio.CancelledError`. A bare handler
that suppresses them keeps a canceled request running or a `Ctrl-C` from
stopping the process.

## When to flag

- A bare `except:` with `pass`, `continue`, `return None`, or a log-and-
  continue body. It hides every failure, including cancellation.
- `except BaseException:` used to keep a worker alive; it catches
  `KeyboardInterrupt` and `SystemExit` as well as task cancellation.
- A task/worker loop that catches `BaseException` and does not re-raise
  `CancelledError`, or catches cancellation explicitly and suppresses it.
- A bare `except:` or `except BaseException:` around an `await` in a cleanup
  path, so a pending `CancelledError` is consumed; the same loss happens when
  a `return`, `break`, or `continue` inside `finally` discards a pending
  cancellation. On Python 3.7 only, `except Exception:` also catches
  `CancelledError`; on 3.8+ it does not.
- A retry loop that catches every exception and retries forever, including
  the cancellation exception.

## Do NOT flag

- `except Exception` that logs and re-raises, or logs and returns an error
  result while preserving the failure for the caller.
- `except (ValueError, KeyError):` with a specific recovery.
- A bare `except:` that immediately re-raises (`raise`) — the bare handler
  is redundant but does not swallow anything.
- A top-level CLI handler that catches `KeyboardInterrupt` to print a message
  and exit with the conventional status 130: that is deliberate control-flow
  handling, not swallowing.
- `except asyncio.CancelledError: raise` or a `finally` block that performs
  cleanup and lets the cancellation propagate.
- Python 3.7 code where `CancelledError` inherited from `Exception`: read the
  project's declared floor before claiming the 3.8 behavior.

## Evidence

Quote the `except` and the body that suppresses. Name the control-flow
exception that is lost and what stays running or unstoppable.
