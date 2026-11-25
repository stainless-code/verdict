---
"@stainless-code/verdict": patch
---

`run --agent agy` now warns at launch that agy's print mode soft-denies file edits outside trusted workspaces, so `review.json` may never be written, and proceeds instead of refusing. `docs/manual.md` documents the two fixes (trust the repository in agy's settings, or pass `-- --dangerously-skip-permissions`) and a working `run --agent agy` recipe.
