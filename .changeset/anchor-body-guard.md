---
"@stainless-code/verdict": patch
---

`post` moves review comments on paths outside the diff index into the
posted body under Unanchored observations instead of submitting inline
anchors GitHub would reject, and splits a body past the 65,536-char
hosting limit across follow-up COMMENT reviews (each part fitting the
limit, footer intact on the first) instead of trimming it. Single-part
reviews post exactly as before.
