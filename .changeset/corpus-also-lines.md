---
"@stainless-code/verdict": patch
---

Corpus cases accept an optional `also_lines` list of alternate anchors for
the same defect. Each entry is matched with the same line tolerance and
defect-naming rule as the primary line, matching any anchor counts the bug
once, and the reported line stays the primary one. The generated
`corpus.schema.json` now requires positive line numbers in `also_lines`
items, so schema validation rejects `0` and negative entries just as
`calibrate` already refused them as `corpus-invalid`.
