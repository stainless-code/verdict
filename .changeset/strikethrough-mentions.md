---
"@stainless-code/verdict": patch
---

Mentions wrapped in strikethrough are escaped too. The neutralizer walked back
over a run of `*` or `_` to tell emphasis markup from the tail of an address,
but `~` is also a valid local-part byte and was left out of that walk, so
`~~@alice~~` was published as written and GitHub read it as a strikethrough
mention — the exact notification the escape exists to prevent. A run of `~~` now
counts as markup the way `**` does, so that shape is escaped while
`foo~@example.com`, `user@example.com`, `(dev@example.com)`, and a lone tilde in
prose such as `approximately ~5 minutes` stay byte-identical. The rewrite
remains idempotent, and fenced code blocks, inline code spans, HTML comments,
and a finding's `suggestion` are still untouched.
