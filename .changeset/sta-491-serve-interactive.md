---
"@stainless-code/verdict": minor
---

`verdict serve` adds local triage and knowledge-base promotion (`POST /v1/triage`, `POST /v1/kb/promote`) through the same services the CLI uses, with inline controls in the embedded UI. A write needs the per-session `X-Verdict-CSRF` token from `/v1/health`, an allowlisted `Origin`, and a strict JSON body. Static exports show no write controls. The write-posture entry says when `serve` writes.
