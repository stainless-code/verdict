---
"@stainless-code/verdict": minor
---

Remove the inactive behavioral-diagram controls: CLI flags `--diagram-behavioral`, `--no-diagram-behavioral`, `--diagram-expanded`, and `--no-diagram-expanded`; personal settings keys `publish.diagrams.behavioral` and `publish.diagrams.expanded`; and environment variables `REVIEW_PUBLISH_DIAGRAMS_BEHAVIORAL` and `REVIEW_PUBLISH_DIAGRAMS_EXPANDED`. Removed flags now produce the normal unknown-flag usage error, and removed settings keys produce `bad-settings`.

Structural diagrams in `report --overview`, including `publish.diagrams.structural` and its CLI and environment controls, remain available.
