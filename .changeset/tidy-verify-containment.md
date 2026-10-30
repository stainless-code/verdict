---
"@stainless-code/verdict": patch
---

Harden the verify verb: the timeout kill now stops the whole process group (a decoy fast-exit can no longer leave orphans behind a recorded success), and known secret values are redacted from the recorded tail. Legitimate commands run unchanged with full env.
