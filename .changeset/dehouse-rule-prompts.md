---
"@stainless-code/verdict": patch
---

Reviews of other repos stop carrying this repository's conventions. The shared
system prompt now defers branch, commit-message, formatting, and style rules to
the target repo's own agent config (`AGENTS.md`/`CLAUDE.md`/`CONTRIBUTING`);
verdict's own rules moved to its `AGENTS.md`, so reviews of this repo keep
them. The prompt-set hash changes with `prompts/system.md`.
