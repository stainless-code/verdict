---
"@stainless-code/verdict": minor
---

Anchors validate against the fetch-time snapshot: `fetch` pins the commentable set and `post`/`record`/`adopt` reuse it (rebuilding only if missing, with a warning); dropped anchors ship as named body notes with reasons instead of failing or misplacing silently. Mid-run pushes read as drift context, not a moving validation target.
