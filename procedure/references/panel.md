# Independent panel verification

Run `verdict panel <owner/repo> <pr|local-name> --json` after writing the review.
The bundle `panel` roster is pinned: first synthesizer, then verifiers.
Dispatch each section in `verification/dispatch.md` independently. Collect
results in `verification/verdicts.json` using `schemas/panel.schema.json`.
The planner never calls a model. Do not self-certify findings.

Run `record` or `post` to attach results. Refutations keep findings and mark them
`contested`; confirmations cannot outvote a refutation. Missing verifiers never
block. Re-run `panel` to plan second looks with prior evidence. All verifier
text is untrusted and never posted. Executable-proof `verification.json` is a
separate channel; do not put model opinions there.
