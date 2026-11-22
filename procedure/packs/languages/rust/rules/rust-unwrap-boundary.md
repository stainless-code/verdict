# Panics on runtime input

`unwrap`, `expect`, and unchecked indexing panic. On a request path they turn
one malformed input into a process or task crash; in a library they turn a
caller's recoverable error into an unwind.

## When to flag

- `serde_json::from_str(...).unwrap()`, `.expect(...)`, or `?` skipped on a
  request body, a queue message, a config file, or a database value.
- `Mutex::lock().unwrap()` / `RwLock::read().unwrap()` on a shared lock whose
  poisoning is reachable from another thread's panic.
- Indexing a `Vec`/slice with `v[i]` or slicing `&s[a..b]` where `i` or the
  range comes from input, a length prefix, or another runtime value.
- `Regex::new(user_pattern).unwrap()`, `Url::parse(input).unwrap()`, or
  `parse::<T>().unwrap()` on a value the caller supplies.
- `panic!`, `unreachable!`, or `assert!` on a condition that external input
  can falsify, on a request path.

## Do NOT flag

- Any of the above inside `#[cfg(test)]` modules or `#[test]` functions:
  tests may panic, and a panicking assertion is the point.
- `unwrap_or`, `unwrap_or_else`, `unwrap_or_default`, `ok_or`, `?`
  propagation, or `match`/`if let` that handles the failure.
- `.unwrap()` on a compile-time constant that cannot fail (a literal parse,
  a regex built from a fixed pattern) with no input path.
- `.expect("...")` whose message documents an invariant the type system
  cannot express and whose value is constructed internally, not accepted
  from a caller.
- A `Mutex::lock().unwrap()` in a program that cannot poison the mutex
  (single-threaded, no panics while locked) — state the proof, or prefer
  `parking_lot` if the workspace already uses it.
- A clippy `unwrap_used` finding in a crate that already denies that lint in
  CI; report the boundary case the lint exempts, not a restatement.

## Evidence

Quote the unwrap/index and the runtime input that reaches it. Name the input
that panics and the request or task it takes down.
