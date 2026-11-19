---
"@stainless-code/verdict": patch
---

The `--spec` checklist cap is now 150 items. The 60-item ceiling refused real specification documents mid-PR; `fetch` and `fetch-local` still refuse `spec-too-large` before writing any bundle file once the combined `--spec` sources enumerate more than 150 items, and the refusal message and hint name the new limit.
