---
"@stainless-code/verdict": minor
---

`verify` refuses before it runs. Destructive, networked, credential-reading,
and repo-escaping commands are rejected with a typed error before execution
(over 40 regression shapes), so a hostile or careless `verifyCmd` cannot
delete, exfiltrate, or escape the repository. The recorded tail also drops
credentials no environment value names — vendor-prefixed tokens,
`Authorization` and `Proxy-Authorization` headers, bare `Bearer` values, and
URL userinfo — and strips ANSI escapes and other control characters, so a
command cannot rewrite the proof it lands in; `.verify.out` keeps the raw
bytes. `doctor` warns on excess token scope, and the manual documents an
opt-in hostile-PR sandbox recipe plus the residual risk a string scan cannot
remove.
