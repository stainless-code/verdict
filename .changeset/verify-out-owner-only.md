---
"@stainless-code/verdict": patch
---

`verify` writes `.verify.out` at `0600` on Unix instead of `0644`: the stream is
the command's raw output, so a command that prints a credential leaves the value
in it, and the file is now the operator's to read and nobody else's — the same
permission `run` gives `.run.out`. The recorded tail in `patch.json` and the
deep-validation receipts stay redacted as before. Windows has no mode bits to
set, so there the file keeps the directory's inherited ACL.
