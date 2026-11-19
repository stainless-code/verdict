---
"@stainless-code/verdict": minor
---

The review handoff is now lean and layered. `<bundle>/procedure.md` carries a pushed core — role, two method lines, the findings-only contract, and a capability map — capped at 3 KB by an archtest. Sections for chunked diffs, prior threads, knowledge-base rules, lenses, and handed context are appended only when the bundle carries that fact. A sliced review checklist and the remaining reference material are written to `<bundle>/refs/` and pulled on demand; the full operator workflow and body-format rules stay with `verdict skill show`. `--show-prompts` prints the assembled handoff and one line naming the refs, never their bodies. Drain and the MCP task prompts use the same core where it applies.

The directive now says to write `review.json` first with empty findings and update it as the review proceeds. When an agent exits without the file, `run` salvages the last fenced `json` block on stdout that is a findings document, records `salvaged: true` in `launcher.json`, and warns. A malformed or absent block keeps the existing `no-review-artifact` refusal; stale artifacts are never overwritten.

`prompts_hash` and the provenance receipt now cover every file that feeds the assembled handoff and its refs, so an edit to any of those bytes is visible in the bundle's recorded prompt set.
