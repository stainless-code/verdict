---
"@stainless-code/verdict": patch
---

`verify` records a tail that drops credentials no environment value names:
vendor-prefixed tokens, `Authorization`/`Proxy-Authorization` headers, bare
`Bearer` values, and URL userinfo are replaced with `***REDACTED***`, and
ANSI escapes plus other control characters are stripped so a command cannot
rewrite the proof it lands in. `.verify.out` still keeps the raw bytes.
