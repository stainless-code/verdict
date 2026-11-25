---
"@stainless-code/verdict": minor
---

`gate --max-severity <tier>` now fails when an open finding at the tier lacks a confirmed cross check, whether the two-finder execution is live, finished, nil, or retired; the evidence dimension names the count. Before, the check ran only while the execution was active, so a finding the panel left pending passed the gate once the execution's artifacts were gone: the findings dimension excludes it as unconfirmed and nothing else asked about it.

A re-fetched bundle keeps the cross requirement. When no live artifact states the capture, a `record.json` or `post.json` receipt for the current head that carries a finder summary decides it, including a two-finder run where one finder failed, and it outranks a configured `finders: 1` default. A finding whose `verification.cross` was stripped is stamped pending instead of reading as a manual finding. A receipt bound to an earlier head no longer makes a clean review of the new head insufficient.

`run --finders 1` now records `finders: 1` in `run.json` on every fresh launch, not only when it retires a prior execution. That explicit count outranks an earlier receipt and leftover execution references, and a manual or `--finders 1` capture drops any stored automatic cross stamp instead of trusting it, so the finding gates as the manual finding it is.
