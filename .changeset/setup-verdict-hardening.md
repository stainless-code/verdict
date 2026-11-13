---
"@stainless-code/verdict": patch
---

Pass setup-verdict inputs through environment variables and cache release archives. On reuse, refresh the signed checksums and build-commit
metadata, verify the archive, then extract a fresh binary without downloading the
archive again. A tampered archive fails closed and remains cached until the entry
is removed. Cache reuse requires release metadata access.
