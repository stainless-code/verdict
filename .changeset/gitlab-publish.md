---
"@stainless-code/verdict": minor
---

`post` publishes to GitLab merge requests: each finding becomes one positioned
discussion, the review body becomes a summary note, and an approval verdict
becomes a real MR approval. `capabilities` reports GitLab's approval and
request-changes support separately, and a request-changes verdict on GitLab
warns that the host has no such event instead of silently pretending.
