---
"@stainless-code/verdict": minor
---

Large PRs now carry the computed change topology in the posted review body, not only in `report --output`. `record` and `post` render the same collapsed `Change topology (computed from the diff)` block when a PR touches more than 8 files or 24 KB, so the map a hosted reviewer sees is the one verdict computed, and the agent never draws one. `--no-overview`, `--no-diagram-structural`, `REVIEW_PUBLISH_DIAGRAMS_STRUCTURAL=false` or `publish.diagrams.structural: false` remove it. The block is capped (60 nodes, 60 edges, 12,000 bytes) with a note naming what was left out, and a body split never cuts it in half. Small PRs are unchanged.
