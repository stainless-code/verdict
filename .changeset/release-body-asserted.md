---
"@stainless-code/verdict": patch
---

The release body is asserted, not assumed. `scripts/release.sh` now reads the published release back after GoReleaser publishes the assets, rewrites the body from `CHANGELOG.md`'s section for the tagged version when it differs, and fails the run when the rewrite does not stick or the release is missing. Release 0.5.0 shipped a one-byte body while the notes file held 15.9 kB and every publish step reported success; the new `body` stage closes that gap, the `release-body` eval holds it offline, and a re-run leaves an already-correct body untouched.
