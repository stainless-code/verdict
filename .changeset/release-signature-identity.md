---
"@stainless-code/verdict": patch
---

A signed install no longer aborts when `cosign` is on the `PATH`: `install.sh`,
`install.ps1`, `verdict upgrade`, and `setup-verdict` now verify the certificate
identity the Release workflow actually mints, which is a push to `main` rather
than the tag the run publishes under. The old check asked for a `refs/tags/…`
identity that no release certificate ever carried.
