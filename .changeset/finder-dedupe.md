---
"@stainless-code/verdict": patch
---

A two-finder `run` no longer reports one defect twice when the finders word it differently. The cross-check request now lists the checker's own findings, and a confirming row may name one as `same_as`; the coordinator merges the pair into one finding (higher severity, the better-evidenced quote, both origins and both checks) only when the other finder's check confirmed the named finding and the two sit in the same file and side at overlapping or adjacent lines. A `same_as` the anchors don't support is ignored with a warning naming why. `verification/plan.json` records each merge under `sources[].merged`, and `post`, `report`, and `gate` count the defect once.
