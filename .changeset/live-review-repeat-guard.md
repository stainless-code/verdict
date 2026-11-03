---
"@stainless-code/verdict": patch
---

`post` refuses exact payloads and same-SHA repeats found in the bundle's
fetched `reviews.json` too, not only in local `.posted_*`/`post.json`
markers. A fresh checkout, a CI re-run on the same commit, or another
machine now refuses a duplicate already posted from that bundle instead of
posting it again; `--force` still overrides, and the refusal hint names the
re-fetch needed when the earlier review was deleted.
