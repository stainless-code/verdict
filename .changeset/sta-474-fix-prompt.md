---
"@stainless-code/verdict": minor
---

Verified `critical` and `important` findings that carry a structured brief now
render a collapsed `Prompt for AI agent` block in the review body, immediately
after `Technical details`. The block is a fenced `text` block, so a developer
copies it in one click; it names the severity, a plain `path:line` location, the
claim, the brief's sites, the required outcome, the suggested approach, and the
open questions. Rendering is deterministic from settled finding data: no model
call and no extra tokens. It is body-only — inline comments keep their finding
text and suggestion.

Eligibility is one fixed predicate: the finding is still `open`, visible,
verified, not refuted by a cross-check, `critical` or `important`, and its brief
outcome is non-empty. `informational` findings, unverified quotes, contested
cross-checks, `deferred`/`addressed` findings, and findings without a brief get
no block. Every dynamic value is escaped as data —
HTML entity-escaped, fences and comments broken, mentions neutralized, no
`[ev-N]` reference — so a hostile brief cannot forge a tag, and the posted-body
splitter keeps each `<details>` region atomic across a 65,536-byte split.

The new `publish.fix_prompt` knob (bool, default `true`) also reads
`REVIEW_PUBLISH_FIX_PROMPT` and the global `--fix-prompt` / `--no-fix-prompt`
flags (flags win over file over env). With the knob off the rendered body is
byte-identical to a render from before the block existed.
