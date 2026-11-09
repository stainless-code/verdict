---
"@stainless-code/verdict": patch
---

Neutralize `@`-mentions in the review body. A body is written by an agent and
published verbatim, so an `@handle` in its prose pinged that person the moment
the review was submitted. `record` now escapes every mention once, where the
body is constructed into the record — the same shared merge `post` runs — so
the recorded body, the `post --dry-run` output, and the payload digest carry
the same bytes instead of diverging at submit time.

The escape is U+200B ZERO WIDTH SPACE between the `@` and the handle: invisible
in rendered Markdown, still plain text, and no host resolves a mention across
it. Fenced code blocks, inline code spans, HTML comments (finding markers
included), and the basis/footer blocks are left byte-identical, and an address
such as `user@example.com` is not a mention because the `@` must follow a space,
a tab, or a line start and the handle must be `[A-Za-z0-9-]+`. The rewrite is
idempotent and has no flag: neutralization is not opt-in.
