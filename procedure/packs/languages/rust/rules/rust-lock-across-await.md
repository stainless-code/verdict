# Locks and borrows held across await

A `std::sync` guard is not designed to cross a suspension point. Holding one
across `.await` makes the future `!Send` (so `tokio::spawn` rejects it) and can
deadlock when the awaited work needs the same lock.

## When to flag

- A `std::sync::MutexGuard`, `RwLockReadGuard`, or `RwLockWriteGuard` whose
  scope spans an `.await`. The guard's `Drop` runs only after the future is
  polled again.
- A `RefCell` `Ref`/`RefMut` held across `.await`: the future becomes `!Send`
  on a multi-thread runtime, and a re-entrant borrow on a current-thread
  runtime panics.
- A `parking_lot` guard held across `.await` unless the crate enables the
  opt-in `send_guard` feature; without it the future is `!Send`.
- A guard stored in a struct or returned in a tuple that outlives the
  function's await points.
- A `std::sync::Mutex` lock taken, then an async call made while the guard is
  still bound, even if the code intends to drop it later.

## Do NOT flag

- A guard explicitly dropped or scoped in a block before the `.await`
  (`drop(guard);` or `{ let g = mutex.lock()?; ... }` then `await`).
- `tokio::sync::Mutex`/`RwLock` guards held across `.await`: those are
  designed for it and their guards are `Send`. Do not report them as
  `std::sync` locks.
- A synchronous function with no `.await`, or a guard held only across
  non-async code.
- A `std::sync` guard in a `!Send` future driven by `LocalSet` or a
  current-thread runtime where the code provably never re-enters the lock and
  the compile-time `Send` failure is not in play. Report the deadlock only
  when the awaited path can re-enter.
- A test that uses `std::sync::Mutex` with no async runtime.

## Evidence

Quote the lock acquisition, the `.await`, and the guard's last use. Name the
future that is not `Send` or the lock the awaited code needs.
