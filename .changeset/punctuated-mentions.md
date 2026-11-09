---
"@stainless-code/verdict": patch
---

Mentions behind punctuation or emphasis are escaped too. The neutralizer opened
a mention only at a line start or after a space or tab, so `(@alice)`,
`**@alice**`, and `_@alice_` were published as written and still notified the
person they named — the exact notification the escape exists to prevent. Any
byte that cannot sit in the local part of an email address now opens a mention,
so those shapes are escaped while `user@example.com` is left byte-identical.
Fenced code blocks, inline code spans, HTML comments including the finding
markers, and a finding's `suggestion` stay untouched, and the rewrite remains
idempotent.
