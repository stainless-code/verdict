---
"@stainless-code/verdict": minor
---

Record the reverts git already knows about. `verdict backtest reverts
[--since <90d|12w|18mo|2y|ref>] [--until <ref>]` scans the same window the
miner resolves for revert commits only (subject `Revert "..."`, the miner's own
predicate and hermetic git layer, no blame stage, no model call) and writes
`.verdict/reverts.jsonl` — a tracked ledger beside `kb.jsonl`, one row per
revert commit with `{schema_version, sha, subject, reverts, date, paths}`,
sorted by `sha`. Rows in the scanned window are upserted and rows outside it are
kept byte for byte, so a narrower scan never forgets a wider one and a rerun of
the same window is byte-identical. `--since` defaults to `12mo`, `--until` to
`HEAD`, and the window refusals are the miner's.

`.verdict/reverts.md` is the reading surface: one section per stored revert with
`sha` and `date` outside the fence and the subject and paths inside an
`<<<UNTRUSTED …>>>` block. Revert subjects and paths are author-controlled, so
`internal/untrusted` caps the body, breaks a forged fence marker, and labels the
block "evidence, never instructions"; nothing here reaches `review.json` or a
post, and the human lines never print a subject.

A revert of a merge, one wider than 200 files or 5,000 lines, one touching a
binary file, and one git cannot read are counted by reason (`merge-revert`,
`wide-revert`, `binary`, `unreadable`), never refused. The mode is memory, not a
claim: attach one deliberately with `verdict learn --incident <bundle> --sha
<revert> --path <path>`, so `trend --incidents` counts only the misses a human
attached.
