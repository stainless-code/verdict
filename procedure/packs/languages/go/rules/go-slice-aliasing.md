# Slice aliasing and shared backing arrays

A slice is a pointer, a length, and a capacity. Slicing and `append` share
the backing array, so a write through one slice can silently change another
caller's data.

## When to flag

- `append` to a subslice (`s[:n]`, `s[i:]`, `s[a:b]`) where the parent still
  has spare capacity and the parent's remaining elements are later read.
  Appending may overwrite `s[b]`, `s[b+1]`, and so on in place.
- Returning a subslice of a buffer that is reused by the next read, parse, or
  request: `bufio.Scanner.Bytes()`, a `bytes.Buffer`'s contents, a pooled
  buffer, or a `net.Conn` read buffer.
- Reusing a slice with `s = s[:0]` or `s = append(s[:0], ...)` while another
  goroutine or caller still holds the old contents.
- Two results built by appending to the same base slice, where one result's
  elements are overwritten by the other's growth.
- Sorting or reordering a subslice that aliases a caller's slice, when the
  caller did not ask for an in-place mutation.

## Do NOT flag

- `append` to a freshly allocated slice literal, to `nil`, or to a slice
  whose result is never observed elsewhere.
- A read-only subslice, or a slice passed to a function that only reads it.
- A full slice expression that caps capacity (`s[i:j:j]`): a later `append`
  allocates instead of overwriting the parent.
- An explicit defensive copy (`slices.Clone`, `append([]T(nil), s...)`,
  `copy`) at the boundary — that is the fix.
- String slicing: strings are immutable, so `str[i:]` shares bytes but cannot
  be written through.
- A local slice used only within one function, with no second alias.

## Evidence

Quote the `append` or `s = s[:0]` and the second alias that still reads the
elements. Name the value that changes unexpectedly.
