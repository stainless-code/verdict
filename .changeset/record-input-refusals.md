---
"@stainless-code/verdict": patch
---

Typed refusals for malformed agent input. `record` (and the shared `post`
validate path) now fail closed with `bad-review-input` on unparsable
`review.json`, non-object `comments[]`/`triage[]`/`sections[]` entries,
non-string `path`/`body`/`suggestion` scalars, non-string `resolved[]`
items, and misshapen `verification.json` claims — where the pipeline used
to crash with no envelope — and with `corrupt-store` on a corrupt or
non-list prior `findings.json`, never starting fresh over history. Nothing
is written on these refusals. An unreadable `source.json` head degrades to
the dirty-head path.
