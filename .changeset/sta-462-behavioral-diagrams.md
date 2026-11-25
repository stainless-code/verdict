---
"@stainless-code/verdict": minor
---

A review may carry one structured behavioral diagram: `review.json`'s optional `diagram`, with `type` (`sequence`, `state` or `flow`), an optional `title`, and `nodes` and `edges` whose `evidence` quotes a diff line the way a spec check does. verdict checks every quote against the diff and draws the picture itself; raw mermaid stays retired. The check proves only that each node and edge cites a real new-side diff line. The relation an edge draws is still the agent's claim. Unanchored elements are dropped, and the block is omitted with a note when fewer than half anchor. Labels keep only letters, digits and simple punctuation, and node ids are generated, so an agent cannot inject mermaid syntax. Caps: 24 nodes, 40 edges, 8,000 bytes per block. The block renders collapsed after the change topology, and its outcome is reported as `evidence.diagram` in `record.json` and `post.json` and as `data.body.diagram` in `post --dry-run`. A malformed diagram is a `diagram-invalid` warning, not a refused review, unless `--strict` or `strict_input` promotes warnings to errors.

Opt in with `publish.diagrams.anchored`, `REVIEW_PUBLISH_DIAGRAMS_ANCHORED=true` or `--diagram-anchored`. It is off by default, and off changes nothing.
