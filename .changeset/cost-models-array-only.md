---
"@stainless-code/verdict": minor
---

**Breaking (cost record):** `cost.json` accepts one `models` shape: an array of entries. A record whose `models` is an object is no longer read by key order — that coercion dropped the keys, which are the only place the writer named a model, so the entry priced without anything able to attribute it. Such a record now reads as unreadable spend, named by the existing `cost.json unreadable — spend unrecorded for this rollup` warning, and the run still completes. Every record the shipped `prompts/review.md` template writes already carries the array form, so no template and no documented recipe changes.
