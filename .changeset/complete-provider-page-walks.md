---
"@stainless-code/verdict": patch
---

Provider page walks finish what they start. A GitLab merge request whose diff
list spans host pages (`glab api --paginate` concatenates one JSON array per
page) now yields every changed file instead of a `PR.json` with no files at
all, and a merged Bitbucket pull request (`state: MERGED`) folds to `merged`
rather than `unknown`. A GitCafe review/comment cursor walk that stops early — a
failed page, an unreadable page, or the 20-page cap — keeps the pages it already
read, warns, and marks `threads.json` `truncated: true` instead of quietly
under-covering the conversation; Forgejo's review list does the same at the
provider cap. Hosts with no failures read exactly as before.
