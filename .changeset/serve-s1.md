---
"@stainless-code/verdict": minor
---

Add `verdict serve`, a read-only HTTP surface over the current repository
(loopback by default; `--host` is the documented exposure opt-in):
`/v1/health`, `/v1/roots`, `/v1/bundles`, the bundle detail, and `show`, with a
per-port session-cookie exchange for the browser, strict CSP, Host and Origin
gates, and `data.sources[]` traceability on every composed response.
