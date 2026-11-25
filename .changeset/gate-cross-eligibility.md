---
"@stainless-code/verdict": minor
---

`gate --max-severity <tier>` now fails when an open finding at the tier lacks a confirmed cross check, whether the two-finder execution is live, finished, nil, or retired; the evidence dimension names the count. Before, the check ran only while the execution was active, so a finding the panel left pending passed the gate once the execution's artifacts were gone: the findings dimension excludes it as unconfirmed and nothing else asked about it.

A re-fetched bundle keeps the cross requirement: when no live artifact states the capture, the finder count a stored `record.json` or `post.json` receipt records decides it, and a finding whose `verification.cross` was stripped is stamped pending instead of reading as a manual finding. A manual or `--finders 1` capture now drops any stored automatic cross stamp instead of trusting it, so the finding gates as the manual finding it is.
