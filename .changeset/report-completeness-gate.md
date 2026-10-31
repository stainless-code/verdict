---
"@stainless-code/verdict": minor
---

Report gate answers "is HEAD reviewed?": new opt-in `--completeness` flag on `report` (alone or with `--check <tier>`) compares the fetched HEAD against the completed-post record, failing closed with a typed error naming the re-fetch command. Without the flag, output and exits are unchanged.
