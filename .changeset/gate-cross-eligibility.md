---
"@stainless-code/verdict": patch
---

`report --check` now fails on a finding the captured two-finder execution left cross-ineligible even when the execution object is nil, finished, or retired, and names how many findings are unresolved instead of leaving the review body's exclusion tag to speak alone.

A finding whose `verification.cross` was stripped can no longer read as a manual finding after the execution that required it is gone: `record`, `post`, and `report` re-derive the stamp from the bound panel artifacts, and a stored receipt that still states two completed finders keeps that requirement alive after `run.json` is re-fetched. A manual or one-finder capture is unchanged and keeps quote-only eligibility; it now also discards any stored automatic cross stamp rather than trusting it.
