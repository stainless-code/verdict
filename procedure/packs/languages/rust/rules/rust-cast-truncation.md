# Narrowing casts on runtime values

`as` between integer types truncates: `u64 as u32` keeps the low 32 bits, and
`i64 as u32` reinterprets the sign. For a length, index, or protocol field
from outside, that silently changes the value instead of rejecting it.

## When to flag

- `value as u32` / `as u16` / `as usize` where `value` is a `u64`/`usize`
  length, offset, count, or wire field taken from input, a file header, or
  another process.
- `u64 as usize` or `usize as u32` in code that must run on 32-bit or wasm32
  targets, where the cast truncates on the narrow target.
- A signed-to-unsigned or unsigned-to-signed cast (`i64 as u64`,
  `u32 as i32`) on a value that can be negative or exceed the destination's
  range.
- A cast used as a bounds check (`if len as u32 <= cap`) where the cast
  itself can wrap, so the check passes for a value that does not fit.
- A narrowing cast on a float-to-integer conversion where the value can be
  NaN or outside the integer range: since Rust 1.45 the `as` cast saturates
  (NaN becomes 0), so report the silent wrong value, not undefined behavior.

## Do NOT flag

- Widening casts that are lossless: `u8 as u32`, `u32 as u64`, `i32 as i64`,
  `usize as u64` on a 64-bit target.
- A cast of a compile-time constant that is provably in range.
- `u32::try_from(value)` / `try_into()` with an error path, or a checked
  conversion via `checked_add`/`checked_mul`.
- A cast in `#[cfg(test)]` code or in a fixture.
- Integer-to-float casts (`u32 as f64`) for values within the exact range;
  precision loss above 2^53 is a numeric issue, not this narrowing rule.
- Pointer casts (`ptr as *const T`) — those belong to the unsafe-invariant
  review, not the integer truncation rule.

## Evidence

Quote the cast and the value's source and destination widths. Name the input
that truncates or changes sign and the wrong length, index, or protocol value
that results.
