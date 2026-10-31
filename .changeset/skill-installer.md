---
"@stainless-code/verdict": minor
---

Copy-based skill installer: new `review.sh install` verb detects the de-facto agent skill dirs (.agents/skills, .claude/skills, .codex/skills at the repo root and $HOME), previews the copy plan for the tool-owned verdict/ payload (SKILL.md + prompts/), and applies only on explicit confirm (same preview-then-confirm idiom as init, typed needs-confirm). Only installer-owned verdict/ dirs (with a .verdict-managed receipt) are ever written — foreign skills and unowned dirs stay byte-identical; re-runs are idempotent and receipt/tool version skew warns instead of failing. Offline, shellcheck-clean, operator docs updated.
