---
"@stainless-code/verdict": minor
---

Record a deterministic `.verdict/state/<bundle>/provenance.json` receipt during `fetch` / `fetch-local` (listing resolved prompt/panel/bundle/KB/learnings/override/control/config/exclusions metadata and `defaults.kb_policy.max_chars` / `REVIEW_KB_MAX_CHARS` truncation without copying secrets or rule text), expose `--provenance` on `report` and `post` (`data.receipt.inputs[]`, `data.truncated[]`, and full human table), and append a single `> Inputs:` summary line to the footer when `provenance.json` is present. Published schema: `data.receipt` (`$defs/provenanceReceipt`) is declared alongside the existing keys, and `data.truncated` widens from `boolean` to `boolean | provenanceTruncatedRow[]` on `reportData` and `postData`, so `ReportData.truncated` and `PostData.truncated` type as a union in the TypeScript client (no compat shim, no `schema_version` bump).
