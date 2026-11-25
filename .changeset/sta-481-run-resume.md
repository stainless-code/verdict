---
"@stainless-code/verdict": minor
---

`verdict run --resume` continues the two-finder execution recorded in the bundle's `launcher.json` instead of starting over. Before launching anything, it checks a fingerprint over the diff, inputs, context, prompt set, head, agent definition, finder topology and review depth, and verifies every bound finder and cross-check artifact. It reuses each phase whose artifact still checks out and re-runs only the incomplete ones. Any drift or damage refuses `resume-drift` (or the spend refusal) and leaves the interrupted record unchanged, so the resume can be retried. A bundle with nothing to continue refuses `cannot-resume`, and no refusal launches an agent. `data.resume` names the reused and executed phases.
