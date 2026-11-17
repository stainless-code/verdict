---
"@stainless-code/verdict": minor
---

**Breaking changes**

The installed skill is now a small Bootstrap skill: it only says when to use Verdict and how to reach the binary. The binary serves the full Procedure, so the instructions always match the version that checks your work.

Reinstall the skill on every machine. For harness installs, run `verdict skill uninstall --agent <x> && verdict skill install --agent <x>` (or `verdict skill install --detect`) for each installed agent. For registry installs, re-run `npx skills add stainless-code/verdict`.

Copies went stale between releases: the Procedure lived inside the installed skill, so an older copy could hand an agent instructions that disagreed with the binary grading its work. Keeping it in the binary removes that skew.
