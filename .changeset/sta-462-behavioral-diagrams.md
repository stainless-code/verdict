---
"@stainless-code/verdict": minor
---

A review may now carry one structured behavioral diagram — `review.json`'s
optional `diagram` object with `type` (`sequence`|`state`|`flow`), an optional
`title`, and `nodes`/`edges` whose `evidence` is exactly the `spec_checks`
evidence shape (`path`, `line`, `quote`). Raw mermaid stays retired: `verdict`
verifies every quote against the pinned `full.diff` and renders the picture
itself. A node whose quote does not verify is dropped, an edge drops with a
failed quote or a dropped endpoint, and the block is omitted whole with one
note below half the elements verified (`MinVerifiedPercent = 50`; exactly half
renders). Labels and titles are reduced to letters, digits, spaces, and a
closed punctuation set, and node ids are generated, so no agent byte can spell
mermaid syntax, a directive, an entity, or a fence. Caps: 24 nodes, 40 edges,
120 runes per label and title, 8,000 bytes per block. The block lands
collapsed right after the computed topology with `Behavior flow (verified
against the diff — N/M elements anchored)`, and the outcome is published as
`evidence.diagram` (type, title, supplied/verified/dropped counts, omission
reason) in `record.json` and `post.json`, and in `post --dry-run`'s
`data.body.diagram`. A malformed block is the `diagram-invalid` warning: it
drops the element or omits the picture, never the review.

Opt in with `publish.diagrams.anchored`, `REVIEW_PUBLISH_DIAGRAMS_ANCHORED=true`,
or `--diagram-anchored` (default off: the body keeps the bytes it had before).
The retired `publish.diagrams.behavioral` and `publish.diagrams.expanded` keys
stay unknown-key refusals, now naming `anchored, structural` as the accepted
keys. `prompts/diagrams.md` documents the structured field instead of the
raw-mermaid wrapper, which moves `prompts_hash`.
