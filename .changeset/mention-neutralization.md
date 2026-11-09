---
"@stainless-code/verdict": patch
---

Neutralize `@`-mentions in the review body and in each finding's inline
comment. Both are written by an agent and published verbatim, so an `@handle`
in that prose pinged that person the moment the review was submitted. `record`
now escapes every mention once, where the text is constructed into the record —
the same shared merge `post` runs — so the recorded body, each finding's posted
comment, the `post --dry-run` output, and the payload digest carry the same
bytes instead of diverging at submit time. A finding's signature is computed
over the escaped bytes, so merging the same bundle twice keeps one signature.

The escape is U+200B ZERO WIDTH SPACE between the `@` and the handle: invisible
in rendered Markdown, still plain text, and no host resolves a mention across
it. Fenced code blocks, inline code spans, HTML comments (finding markers
included), and the basis/footer blocks are left byte-identical, and an address
such as `user@example.com` is not a mention because the `@` must follow a space,
a tab, or a line start and the handle must be `[A-Za-z0-9-]+`. A finding's
`suggestion` is code and stays byte-identical too — a zero-width space inside a
patch would be an edit nobody can see. The rewrite is idempotent and has no
flag: neutralization is not opt-in.
