---
"@stainless-code/verdict": patch
---

`run --agent agy` now prints a one-line warning on stderr at launch (`warn: agy print mode (-p) soft-denies file edits outside trusted workspaces, so review.json may never be written; trust this repository in agy's settings or pass -- --dangerously-skip-permissions (your approval bypass)`) and proceeds rather than refusing. `docs/manual.md` documents the edit soft-denial, its two operator-side remedies (`trustedWorkspaces` / `permissions.allow` in `~/.gemini/antigravity-cli/settings.json`, or `-- --dangerously-skip-permissions`), and the working `run --agent agy` recipe.
