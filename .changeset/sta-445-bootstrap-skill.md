---
"@stainless-code/verdict": minor
---

**Breaking changes**

The installed skill is now a small Bootstrap skill: it only says when to use Verdict and how to reach the binary. The binary serves the full Procedure, so the instructions always match the version that checks your work.

Reinstall the skill on every machine. For harness installs, for each installed agent, run `verdict skill status --agent <x>` and delete the old `verdict` skill directory at the path printed after `->`; then run `verdict skill install --agent <x>`. `verdict skill install --detect` is fine instead of `--agent`. `verdict skill uninstall` removes only the new Bootstrap skill files, so it would leave the old Procedure files beside the stub.

For registry installs, delete the old `verdict` skill directory, then re-run `npx skills add stainless-code/verdict`.

Copies went stale between releases: the Procedure lived inside the installed skill, so an older copy could hand an agent instructions that disagreed with the binary grading its work. Keeping it in the binary removes that skew.
