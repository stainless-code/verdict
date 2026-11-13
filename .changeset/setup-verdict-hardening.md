---
"@stainless-code/verdict": patch
---

Pass setup-verdict inputs through environment variables and verify cached binaries
against a freshly downloaded, checksum- and signature-verified release before use.
Tampered cache entries now fail closed. Cache reuse requires release access.
