---
"@stainless-code/verdict": patch
---

Pre-commit runs `shellcheck -S warning` over staged shell files (missing binary skips with
a notice, same degrade-gracefully idiom as the formatter check), and the CI lint job covers
every tracked `*.sh` instead of three hardcoded paths. Pinned by a hermetic hook eval
(violate fails, clean passes, missing binary skips).
