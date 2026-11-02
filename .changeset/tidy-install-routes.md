---
"@stainless-code/verdict": minor
---

Settle one execution model across install routes. The copy-based `install` verb now ships the CLI (`scripts/`) inside the installed `verdict/` skill copy, so SKILL.md command paths resolve with no checkout beside it; the README routes registry users to that bundled script and names `install.sh` source-checkout-only (it refuses elsewhere, and stays out of the npm tarball with `hooks/`); SKILL.md, the install help, and the manual agree verbatim that `.review/` is cwd-local. New `install-routes` eval proves each route hermetically.
