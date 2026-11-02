---
"@stainless-code/verdict": minor
---

Explicit offline posture for the `report` gate's live-thread dimension. `report --check/--completeness` gains `--live auto|strict|skip`: `auto` (default) keeps today's fail-open behavior, `strict` fails the gate closed when the live lookup is unreachable, and `skip` pays zero network and gates on the local dimensions only, saying so loudly in the output and `--json` warnings. The manual documents the fail-open default and its reasoning where the recovery is documented.
