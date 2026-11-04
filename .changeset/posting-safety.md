---
"@stainless-code/verdict": minor
---

Posting is harder to get wrong and harder to do twice. `post` refuses a second
review on the same reviewed SHA before any network read, naming the exact
`fetch --base-sha` advance; it also refuses duplicates found in the bundle's
own fetched review list, so a fresh checkout, a CI re-run, or another machine
cannot repost what the PR already has — `--force` still overrides, and the
refusal names the re-fetch needed when the earlier review was deleted. Points
outside the diff hunks post under "Unanchored observations" instead of as
inline anchors GitHub would reject. A body past the 65,536-character hosting
limit splits into follow-up COMMENT reviews — first review keeps the footer,
later parts carry a continuation header, and a failed follow-up refuses typed
naming the review that already landed — instead of being trimmed. A 403/429
carrying a rate-limit signal retries within bounds, honoring `retry-after` or
the reset marker and never publishing twice; when the last permitted attempt
is still retryable, the refusal carries `retry_at` in RFC 3339 so an agent can
wait for that instant.

A stale-fetch downgrade note is now followed by a blank line, like the drift note, instead of a stray literal `\n` in the posted review body.
