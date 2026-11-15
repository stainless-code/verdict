---
"@stainless-code/verdict": patch
---

Retry an inconsistent `pr-wait` observation instead of failing the whole
bounded wait: a GraphQL head that is not yet the REST head (or a torn view
document) counts as an inconsistent observation, the wait sleeps one interval
and reads again, and only `--once`, a hard `gh` failure, or a state still
inconsistent at the timeout refuses `pr-read-failed`. The pr-wait data now
carries `inconsistent_observations` so a retried read stays visible.
