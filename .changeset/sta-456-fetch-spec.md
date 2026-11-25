---
"@stainless-code/verdict": minor
---

`fetch` and `fetch-local` accept a repeatable `--spec <file>` to hand specification documents into the bundle, separate from `--context` reference material. Validation matches `--context`. Spec sources share the `context/` directory and `context.txt`, `context.json` marks each entry `"role": "spec"` or `"role": "context"`, and `toc.txt` marks spec rows `[spec]`. Combined spec sources are capped at 150 checklist items; more refuses `spec-too-large` before any bundle file is written.
