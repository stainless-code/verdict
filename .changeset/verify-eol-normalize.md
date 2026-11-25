---
"@stainless-code/verdict": patch
---

`verify` and `fetch-local` now name the content as `git add` would commit it. They apply git's built-in line-ending cleaning (`core.autocrlf`, `core.eol`, the `text`/`eol`/`binary` attributes) instead of hashing the raw working-tree bytes. A CRLF checkout of an LF commit, which is Git for Windows' default, now names that commit's tree. PR-bundle `VERIFIED` claims and `verify --group` runs now bind there instead of refusing. A working-tree bundle's diff no longer shows every CRLF line as changed.

The snapshot holds the cleaned bytes, so on such a checkout the command runs on LF files, as CI does. `binary` and `-text` files keep their exact bytes. No filter program runs: every configured clean, smudge, or process driver, git-lfs included, is switched off. A smudged Git LFS file therefore still names different content from the commit that stores its pointer. `verify` now warns about this and names LFS and the paths, and `verify --group` refuses `head-mismatch` with a message that names LFS instead of asking for a clean checkout.
