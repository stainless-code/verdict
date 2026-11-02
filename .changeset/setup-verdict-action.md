---
"@stainless-code/verdict": minor
---

`setup-verdict` GitHub Action. `stainless-code/verdict/setup-verdict@vX`
installs a pinned, signature-verified `verdict` in CI on Ubuntu, macOS, and
Windows runners: it reuses the `install.sh`/`install.ps1` verifier
(checksum plus cosign identity/issuer, fail-closed), never touches shell rc
files (PATH goes through `$GITHUB_PATH`), reuses the version-scoped binary
within a job without re-downloading, and exposes `path` and `version`
outputs. A hermetic workflow tests it against locally built fake release
assets, so no test touches real GitHub releases.
