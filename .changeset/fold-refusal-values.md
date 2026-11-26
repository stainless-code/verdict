---
"@stainless-code/verdict": patch
---

A `record`, `post`, or thread refusal that quotes text from a review, a bundle file, a PR, or a host now folds the line breaks in that value to spaces. Such a value — a `review.json` `schema_version`, a triage status, a changed file's path, a host's PR state or review id — could end the `error:` line and start a second one that read as verdict's own. The value is still named. The `--json` refusal message carries the same folded text.
