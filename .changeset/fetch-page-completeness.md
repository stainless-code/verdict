---
"@stainless-code/verdict": patch
---

Read complete provider lists instead of the first page. `fetch` pages
Forgejo `/files` and `/reviews` (`limit=50`), GitLab MR notes
(`per_page=100` through `glab api`), and Azure change entries
(`$top`/`$skip`), all behind one shared page cap. A hit cap, a failed page, or
a failed review-thread read lands an envelope warning (and an empty thread
file where it already wrote `[]`) instead of silently under-covering.
