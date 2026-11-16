---
"@stainless-code/verdict": patch
---

**Quiet local `report`** — a local bundle's live recheck is a structural skip,
so `report` now prints one `info:` line on the human stream instead of warning
about it. The skip stays out of `--json` `warnings[]`; a failed live read on a
remote bundle still warns.
