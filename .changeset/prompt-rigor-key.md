---
"@stainless-code/verdict": patch
---

Publish the review.json key names the prompt only described. `prompts/review.md`
now tags a finding's evidence rung in the comment key `rigor`, publishing its
values as `said|pointed|walked|ran|reproduced`, writes the in-bounds flag as
`in-bounds`, and shows a coverage entry as `{"file": …, "ranges": …}`; the
`SKILL.md` router names `rigor` too. A pilot review emitted `rung` and
`in_bounds` — the aliases `record` repairs as warnings, which strict CI refuses
— because no prompt published the keys the schema defines.

An archtest holds every key the prompt publishes to review.schema.json and its
`rigor` value list to the schema enum, so the two cannot drift apart again. The
prompt set moves `prompts_hash` to
`e62e67fb077675601eec7b83f6f6e8e1ea0bfac881e82d3db192d840b6ab6f83`, and the
record and fetch goldens follow it: `prompts_hash`, the `run_id` that folds it,
and the two edited prompt rows of each provenance input list.
