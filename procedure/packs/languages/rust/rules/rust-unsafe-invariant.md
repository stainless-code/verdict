Report an `unsafe` block with no stated invariant: a raw pointer dereference,
`transmute`, `from_utf8_unchecked`, `set_len`, or `get_unchecked` where bounds,
alignment, aliasing, and lifetime are not argued in a comment next to the block.
An unproven safety comment is the finding.
