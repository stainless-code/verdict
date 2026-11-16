---
"@stainless-code/verdict": patch
---

**Generated-path coverage exemption** — `*.gen.*` and `zz_generated.*` paths
now count as generated in the shared role heuristic, so `next --coverage` and
the risk floor exempt them exactly as the review prompt promises.
