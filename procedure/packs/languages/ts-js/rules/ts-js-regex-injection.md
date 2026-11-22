# Regular expressions built from untrusted input

`new RegExp(pattern)` compiles whatever string it is given. An attacker can
change the match semantics, inject flags, throw a `SyntaxError` on an
unbalanced pattern, or supply a pattern whose backtracking stalls the event
loop (ReDoS).

## When to flag

- `new RegExp(value)` or `RegExp(value, flags)` where `value` or `flags`
  comes from a request, query, config supplied by a tenant, or a template
  field, and the value is not escaped.
- A search or filter endpoint that compiles the user's query as a pattern
  (`{$regex: req.query.q}` in MongoDB, `new RegExp(q, "i")` in an
  application filter).
- A pattern assembled by concatenation from input and then used with
  `.test`/`.exec`/`.match`/`.replace` on unbounded input.
- A user-supplied `flags` string: flags are not data, and `g`/`y` change
  state across calls.
- A catastrophic-backtracking pattern on attacker-sized input in a
  long-lived handler, even when the pattern is static, when the diff adds the
  unbounded input path.

## Do NOT flag

- A regular-expression literal, or `new RegExp` over a module constant.
- A pattern built from input escaped for regular-expression syntax, with
  `RegExp.escape` (where the runtime ships it), a vetted `escapeRegExp`
  helper, or a character-class allowlist.
- A call that only checks a fixed prefix/suffix with `startsWith`/
  `endsWith`/`includes`, which take plain strings.
- `String.prototype.replaceAll` or `.split` with a string argument.
- A static pattern applied to a bounded, validated value such as a UUID.

## Evidence

Quote the `RegExp` construction and the input path. Name the effect:
semantics change, thrown `SyntaxError`, flag injection, or backtracking on a
specific input shape.
