---
"@stainless-code/verdict": minor
---

A review may now carry one structured behavioral diagram — `review.json`'s
optional `diagram` object with `type` (`sequence`|`state`|`flow`), an optional
`title`, and `nodes`/`edges` whose `evidence` is exactly the `spec_checks`
evidence shape (`path`, `line`, `quote`). Raw mermaid stays retired: `verdict`
checks every quote against the pinned `full.diff` and renders the picture
itself. What the check proves is narrow and stated as such: every drawn node
and edge cites a real NEW-side diff line inside a hunk (added or context); the
relation an edge draws stays the agent's claim. A node whose quote does not
match is dropped, an edge drops with a failed quote or a dropped endpoint, and
the block is omitted whole with one note when fewer than half the elements
anchor (`MinVerifiedPercent = 50`; exactly half renders). Labels and titles are
reduced to letters, digits, combining marks, spaces, and `.,:!?'()+_-/@` — no
other rune — and node ids are generated, so no agent byte can spell mermaid
syntax, a directive, an entity, or a fence; an out-of-scope type appears in the
note inside a code span, so it never links. Caps: 24 nodes, 40 edges, 120 runes
per label and title, 8,000 bytes per block, with a `%%` note that also counts
edges the node cap orphaned. The block lands collapsed right after the computed
topology as `Behavior flow (each element anchored to a diff line — N/M
anchored)`, and the outcome is published as `evidence.diagram` (type, title,
supplied/verified/dropped counts, omission reason) in `record.json` and
`post.json`; `post --dry-run` reports the same fields plus `status` in
`data.body.diagram` (`$defs/postDiagram`, `null` when nothing is drawn). A
malformed block is the `diagram-invalid` warning: it drops the element or omits
the picture, never the review.

Opt in with `publish.diagrams.anchored`, `REVIEW_PUBLISH_DIAGRAMS_ANCHORED=true`,
or `--diagram-anchored` (default off: the body keeps the bytes it had before).
The retired `publish.diagrams.behavioral` and `publish.diagrams.expanded` keys
stay unknown-key refusals, now naming `anchored, structural` as the accepted
keys. `prompts/diagrams.md` documents the structured field instead of the
raw-mermaid wrapper, which moves `prompts_hash`.
