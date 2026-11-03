---
"@stainless-code/verdict": minor
---

One front door: the `verdict` binary owns every verb, and the old shell entry
point is gone. Usage errors, `re-enter:` lines, and re-fetch hints name
`verdict` and a subcommand instead of a script path, and `scripts/review.sh` no
longer ships — the registry tarball carries the binary and the skill payload,
and a stale manifest still listing the removed script fails the tarball
contract.
