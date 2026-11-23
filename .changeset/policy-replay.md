---
"@stainless-code/verdict": minor
---

`report --policy <file>` replays a stored bundle under an alternative policy: it derives the gate and approval twice — once under the captured policy, once under the candidate — from the same findings, checks, and receipts, and prints both decisions plus the dimensions that changed. The candidate is validated with the config schema and resolver. The replay writes nothing, calls no host, and exits 0 whatever the candidate decides.
