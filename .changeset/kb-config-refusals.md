---
"@stainless-code/verdict": patch
---

A finding that cites a pack rule `fetch` recalled into the bundle's `kb.txt`, such as the built-in `sec-injection`, keeps that cite: `record` no longer warns `ignoring unknown kb rule` and drops it, with or without a `.verdict/kb.jsonl`. A flip of such a finding credits nothing and warns nothing, because pack rules carry no counters. An id the bundle never listed and the store does not hold still warns.

A configuration parse refusal now names the file it read: `.verdict/config.json: schema_version: missing format marker, …`, and `settings file <path>: …` for the personal settings file. A `fetch` or `fetch-local` that refuses no longer leaves a half-written bundle: the bundle directory the run created is removed. A refused re-fetch keeps the bundle that was already there.
