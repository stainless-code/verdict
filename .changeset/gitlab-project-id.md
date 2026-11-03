---
"@stainless-code/verdict": patch
---

Look up the GitLab project id with the plain `owner%2Frepo` path. The
bash-parity port encoded `owner%2Frepo%0A` (the herestring newline), and
GitLab does not trim it, so the project lookup could miss and fail a GitLab
fetch at the first step.
