---
"@stainless-code/verdict": minor
---

The readiness gate can say what it knows. `report --check` and
`report --completeness` gain a live-thread dimension: open threads the local
store does not know about fail the gate, and unreachable lookups warn and stay
neutral. New `--live auto|strict|skip` picks the posture — `auto` keeps that
fail-open default, `strict` fails closed when the host cannot be reached, and
`skip` pays zero network and gates on the local dimensions only, saying so in
the output and the `--json` warnings. `post` and the gate now name the same
advance for head drift, and a binding APPROVE on a known-stale fetch
downgrades to COMMENT instead of approving past the moved head.
