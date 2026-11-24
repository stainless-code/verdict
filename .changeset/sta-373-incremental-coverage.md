---
"@stainless-code/verdict": minor
---

An incremental (`fetch --base-sha`/`--since`) delta review is now graded against the full hunks plus the prior round's recorded attestation in `prior-coverage.json`: unchanged ranges stay credited, so the agent attests only the delta, ranges the prior never covered remain gaps, and an absent or unreadable prior credits nothing — grading fails closed against the full hunks. Lines the delta re-modifies lose their prior credit and must be re-attested. Fetching without `--base-sha`/`--since` clears any stale `incremental.diff`. Full-diff rounds are unchanged. `record` persists the attested union on every successful non-chunked run with at least one attested line so the next round can credit it.
