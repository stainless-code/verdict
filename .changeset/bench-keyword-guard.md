---
"@stainless-code/verdict": patch
---

Refuse a corpus keyword the case's own diff spells, including as one part of a
split selector, a spaced selector, or a prefix of a longer identifier. The
built-in `kind` and category phrases the diff spells are instead dropped from
that case's grading, so race and bounds cases stay curatable; a case left with
no phrase is refused and asks for a keyword the diff does not spell. Grading
also normalizes the finding's text, so a phrase that opens a sentence
("Empty positional: ...") still names the defect.
