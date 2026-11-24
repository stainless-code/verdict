---
"@stainless-code/verdict": minor
---

`verdict serve --interactive` adds two local-only write endpoints, `POST
/v1/triage` and `POST /v1/kb/promote`, through the same triage and learn
services the CLI calls (byte-identical `findings.json` and `.verdict/kb.jsonl`
writes). Default `serve` stays strictly GET-only; the writes are gated by a
per-session `X-Verdict-CSRF` token published on `/v1/health`, a present
allowlisted `Origin`, and strict JSON bodies. The embedded UI shows inline
triage controls and a KB promotion form only when the server advertises
interactive mode (never in static exports).
