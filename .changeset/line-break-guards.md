---
"@stainless-code/verdict": patch
---

Refuse Unicode line breaks where only C0 controls were refused before. A URL
path, a learning's cited path, and a rendered `pack test` case line each tested
`unicode.IsControl`, which excludes U+2028 and U+2029 — runes a renderer treats
as a newline. Those characters now refuse the `serve` URL before routing
(`404 no such route`), refuse a learning path as `bad-scope` with the message
naming line breaks, and flatten to `�` in the `pack test` line. Parent
components, glob syntax, and every earlier refusal are unchanged.
