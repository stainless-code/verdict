---
"@stainless-code/verdict": minor
---

The review handoff is now lean and layered. `<bundle>/procedure.md` carries a pushed core — role, two method lines, the findings-only contract, and a capability map — capped at 3 KB by an archtest. Sections for chunked diffs, prior threads, knowledge-base rules, lenses, and handed context are appended only when the bundle carries that fact. A sliced review checklist and the remaining reference material are written to `<bundle>/refs/` and pulled on demand; the full operator workflow and body-format rules stay with `verdict skill show`. `--show-prompts` prints the assembled handoff and one line naming the refs, never their bodies. Drain and the MCP task prompts use the same core where it applies.

The directive now says to write `review.json` first with empty findings and update it as the review proceeds; after a clean exit only (no crash, timeout or non-zero exit), `run` salvages the last fenced `json` block on stdout that decodes to an object with a `findings` array, adding `schema_version` "1" if missing, records `salvaged: true` in `launcher.json`, and warns. It replaces only an absent artifact or the exact empty placeholder; a salvaged or written review that fails the shared checker is refused `bad-review-input` with its problems, which feed the next round under `--rounds`, while only stdout with no such block keeps `no-review-artifact`.

A finished review with no findings must carry a non-empty `summary`; the empty placeholder alone is never a clean review, and `run` checks every artifact with the same checker `record` uses before reporting success.

`prompts_hash` and the provenance receipt now cover every file that feeds the assembled handoff and its refs, so an edit to any of those bytes is visible in the bundle's recorded prompt set.
