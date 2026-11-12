---
"@stainless-code/verdict": patch
---

`verdict pack add` reports a failed sweep of the previous tree as success plus a warning
that names the leftover dot-prefixed sibling, instead of refusing an add whose new tree
and lock are already durable. The loader already skips that sibling.
