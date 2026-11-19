---
"@stainless-code/verdict": minor
---

`fetch` and `fetch-local` now accept a repeatable `--spec <file>` flag to hand
specification documents into the bundle separately from plain `--context`
reference material.

- Validation matches `--context` (size, type, symlink, and depth caps).
- Spec sources are stored in the same `context/` directory and fenced
  `context.txt`, while `context.json` records `"role": "spec"` or
  `"role": "context"` per entry in place and `toc.txt`'s `## context` section
  marks spec rows with `[spec]`.
- Combined spec sources are capped at 60 enumerated checklist items; exceeding
  the cap refuses `spec-too-large` with no bundle files written.
