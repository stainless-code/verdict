---
"@stainless-code/verdict": minor
---

verdict now checks the evidence a review states before deciding. Each finding's `quote` must match the diff at its `path:line`, a review whose findings are all below the approval tier must receipt every changed hunk, and `--spec` sources get a per-item checklist. The result is stored in `record.json` and read by every verb: `record --dry-run` names each gap, `run` repairs gaps across rounds and refuses `review-insufficient` if any remain, `gate` fails an always-on `evidence` dimension, and `post` publishes an insufficient review with that lead and never approves.

**Breaking:** a review that states no evidence is insufficient, not approved.

- Quote the exact line at each finding's `path:line`. A multi-line quote adds `start_line` and one quoted line per line in the range.
- A review with no gating finding must carry `receipts[]`: one `{"hunk": "<the hunks.tsv row>", "quote": "<a line from that hunk>"}` per changed hunk. `record --dry-run` names every gap.
- `run --rounds` defaults to 2, so an invalid review is re-prompted once. Pass `--rounds 1` for the old behavior.
- `gate` grades a seventh dimension, `evidence`: a bundle with no evidence receipt for its head fails as `unknown`.
- A `--spec` item with no check, or backed only by test output (logs, goldens, snapshots), withholds approval.
