---
"@stainless-code/verdict": minor
---

`post` now submits to Bitbucket Cloud: each inline finding lands as an anchored
comment (`inline: {path, to}` for a new-side line, `{path, from}` for an
old-side line), the summary comment carries the review body, and a binding
approval lands last via `POST .../pullrequests/<n>/approve`. Like GitLab,
Bitbucket has no request-changes review event, so a bound request-changes
posts the comments and warns that the verdict did not bind. Anchors keep the
same honesty as every host — a finding with no placeable anchor is withheld
and counted, never mis-anchored — and the run keeps the duplicate-post,
stale-head, and SHA-bound receipt guards every other host carries.
