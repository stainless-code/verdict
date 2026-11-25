---
"@stainless-code/verdict": minor
---

Verified `critical` and `important` findings with a structured brief now render a collapsed `Prompt for AI agent` block in the review body, right after `Technical details`. It is a fenced `text` block a developer copies in one click: the severity, the `path:line`, the claim, the brief's sites, the required outcome, the suggested approach and the open questions. It is rendered from the settled finding, with no model call, and appears in the body only; inline comments are unchanged.

A finding gets the block only if it is open, visible, verified, not refuted by a cross-check, `critical` or `important`, and has a brief outcome. Every value is escaped, so a hostile brief cannot forge markup, and a body split keeps each `<details>` block whole.

Turn it off with `publish.fix_prompt: false`, `REVIEW_PUBLISH_FIX_PROMPT=false` or `--no-fix-prompt`; off, the body is unchanged.
