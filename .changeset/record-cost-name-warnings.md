---
"@stainless-code/verdict": patch
---

`record` (and `record --dry-run`) now warns once per `cost.json` `models[]`
entry with no `name`, for example `cost.json: models[0] has no name — the name
is what attribution reads; add "name"`. The record stays accepted and priced —
warnings only, never a refusal or rewrite — and `record --json` carries the
same text in `warnings[]`. Only a missing or non-string name warns; an empty
string passes, since the schema sets no minimum length.
