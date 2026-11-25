---
"@stainless-code/verdict": minor
---

`post` now submits to Azure DevOps. Each finding becomes a review thread anchored on its file line (new or deleted side), and one general thread carries the review body. Every call is an `az rest` call after `az login`. No token reaches an argv or an error, and request bodies go through private `0600` files. `AZURE_DEVOPS_ORG` must be `dev.azure.com` or `{org}.visualstudio.com`, or the post refuses before `az` runs. Azure has no review event, so a verdict never approves or requests changes there: the threads and body post with a warning and a `withheld` entry in `post.json`. A finding without a placeable anchor is withheld and counted, `post` refuses when the head moved since the fetch, and a re-run updates its own summary thread instead of adding another.
