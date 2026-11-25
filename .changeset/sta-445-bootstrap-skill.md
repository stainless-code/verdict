---
"@stainless-code/verdict": minor
---

**Breaking:** the installed skill is now a small bootstrap skill that only says when to use verdict and how to reach the binary. The binary serves the full Procedure, so the instructions always match the version that checks the work; before, an older installed copy could disagree with the binary grading it.

Reinstall the skill on every machine. For each installed agent, run `verdict skill status --agent <x>`, delete the old `verdict` skill directory at the path it prints after `->`, then run `verdict skill install --agent <x>` (or `--detect`). `verdict skill uninstall` removes only the new files, so it would leave the old ones behind. For a registry install, delete the old `verdict` skill directory and re-run `npx skills add stainless-code/verdict`.
