---
"@stainless-code/verdict": minor
---

Record a deterministic `.verdict/state/<bundle>/provenance.json` receipt during `fetch` / `fetch-local` (listing resolved prompt/panel/bundle/KB/learnings/override/control/config/exclusions metadata and `defaults.kb_policy.max_chars` / `REVIEW_KB_MAX_CHARS` truncation without copying secrets or rule text), expose `--provenance` on `report` and `post` (`data.receipt.inputs[]`, `data.truncated[]`, and full human table), and append a single `> Inputs:` summary line to the footer when `provenance.json` is present.
