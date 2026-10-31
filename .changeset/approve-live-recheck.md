---
"@stainless-code/verdict": minor
---

Binding APPROVE now rechecks live open threads before posting: unresolved live threads the local store doesn't know about refuse APPROVE with a typed error (triage or `--force`); an unreachable lookup degrades to COMMENT with the reason stated instead of approving blind. COMMENT, dry-run, and local flows make no new network calls.
