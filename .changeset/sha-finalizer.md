---
"@stainless-code/verdict": minor
---

Exactly one terminal write, enforced: `post` refuses a second completed post on the same reviewed SHA with a typed error (`--force` overrides, new commits re-arm automatically). Dry runs, attempts, and failures never arm the guard. The existing exact-payload replay guard stays as the fine layer underneath.
