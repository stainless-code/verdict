---
"@stainless-code/verdict": minor
---

SHA-finalizer and APPROVE-path correctness. A second `post` on the same reviewed SHA is refused naming the exact `fetch --base-sha` advance; post-time drift and the `report --check` gate name the same advance, and a binding APPROVE on a known-stale fetch downgrades to COMMENT. `report --check` gains a live-thread dimension: unknown-to-store open threads fail the gate, unreachable lookups warn and stay neutral.
