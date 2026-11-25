---
"@stainless-code/verdict": minor
---

**Breaking:** `REVIEW_PUBLISH_ATTRIBUTION=true|false` is no longer accepted (it warned and mapped to `product`/`off`), and a boolean `publish.attribution` in the settings file now refuses with the generic string-type message. Migrate to `off|product|full`.
