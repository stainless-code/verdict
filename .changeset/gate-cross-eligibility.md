---
"@stainless-code/verdict": minor
---

**Behavior change:** `gate --max-severity <tier>` now fails when an open finding at the tier lacks a confirmed cross-check, whether the two-finder execution is live, finished or gone; the evidence dimension names the count. Before, a finding the panel left pending passed once the execution's artifacts were gone.

A re-fetched bundle keeps the requirement: a `record.json` or `post.json` receipt for the current head that names a finder summary decides it, even when one finder failed, and it outranks a configured `finders: 1` default. A receipt bound to an earlier head no longer makes a clean review of the new head insufficient. `run --finders 1` now always records `finders: 1` in `run.json`, so a single-finder capture gates as the manual finding it is.
