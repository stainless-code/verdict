---
"@stainless-code/verdict": minor
---

Add `verdict serve`, a read-only loopback HTTP surface over the current
repository: `/v1/health`, `/v1/roots`, `/v1/bundles`, the bundle detail, and
`show`, with a one-time token exchange for a per-port session cookie, strict
CSP, Host and Origin gates, and `data.sources[]` traceability on every composed
response.
