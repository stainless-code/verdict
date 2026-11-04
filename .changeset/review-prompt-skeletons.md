---
"@stainless-code/verdict": patch
---

The shipped review prompts no longer hand an agent a corrupt skeleton to copy.
The "Technical details" block and the mermaid wrapper had a fence opened mid-line
with its body flattened onto it, plus orphan closers and an unclosed block, so an
agent following them emitted run-together headings and an unterminated fence.
Both are well-formed blocks now.
