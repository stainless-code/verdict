---
"@stainless-code/verdict": patch
---

Prompt-dump missing files fail loudly. `--show-prompts` no longer skips absent trusted prompt files silently: a missing `prompts/system.md` refuses typed (`prompt-dump-failed`, since the untrusted-data preamble lives there), and a missing `SKILL.md`, `prompts/review.md`, or `prompts/diagrams.md` warns (stderr plus envelope `warnings[]`) while the dump proceeds. Missing bundle files still skip silently.
