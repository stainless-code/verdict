---
"@stainless-code/verdict": minor
---

Large PRs now carry the computed structural topology in the posted review body, not only in `report --output`. `record` and `post` render the same collapsed `<details>` block — `<summary>Change topology (computed from the diff)</summary>` — from one builder under `internal/diagram`, under the same over-8-files-or-24KB trigger, so the map verdict draws is the map a hosted reviewer sees and the agent never draws one.

The block is gated by both `publish.overview` and `publish.diagrams.structural` (default `true` each): `--no-overview`, `--no-diagram-structural`, `REVIEW_PUBLISH_DIAGRAMS_STRUCTURAL=false`, and `publish.diagrams.structural: false` all remove it, and a below-threshold PR posts no block even with `--overview`, because the body honours the size gate strictly. Capping keeps the block postable: at most 60 file nodes and 60 edges in bundle order, a 180-byte ceiling on one label, and a 12,000-byte ceiling that drops whole nodes or edges before the closing fence — one mermaid `%%` comment names what the caps left out, and edges only join nodes the block renders, so a truncated graph never points at a node it did not draw. The posted-body splitter now treats a `<details>` region as one unit, so a 65,536-byte split never cuts a diagram in half.

Small PRs stay byte-identical: the block appears only past the size gate.
