---
"@stainless-code/verdict": patch
---

Refuse every vocabulary escape in a real corpus case's own diff: a keyword that
is one part of a split selector, a spaced selector, a prefix of a longer
identifier, or the bug's kind or category phrase is no longer a free pass.
Normalize a finding's text before matching its keywords, so a phrase that opens
a sentence ("Empty positional: ...") still names the defect.
