---
"@stainless-code/verdict": minor
---

An incremental (`fetch --base-sha`/`--since`) delta review is now graded against the incremental diff plus the prior round's recorded attestation in `prior-coverage.json`: ranges the recorded prior already covered stay credited, so the agent attests only the delta, ranges the prior never covered remain gaps, and an absent or unreadable prior credits nothing — grading fails closed against the full hunks. Full-diff rounds are unchanged. `record` persists the attested union on every successful non-chunked run so the next round can credit it.
