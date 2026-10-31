---
"@stainless-code/verdict": minor
---

Change overview: `report --output` renders a script-computed collapsed file table plus a `flowchart TD`
change-topology diagram on large diffs (more than 8 files or 24KB, `--overview`/`--no-overview` override),
with stable F-row ids, deterministic relations only, and a new `publish.overview` knob.
