---
"@stainless-code/verdict": minor
---

`verdict report <repo> <pr|slug> --compare <baseline>` compares finding signatures with a prior bundle (a directory, or a PR or `local-<name>` in the same `.verdict/state`) in four buckets: `new`, `persisting`, `resolved` and `not reviewed`. Persisting rows show severity and status moves. A finding counts as resolved only when attested coverage of its hunk shows it gone or the PR's diff no longer touches it; an incremental delta, an excluded file, an unattested hunk or a missing snapshot reads `not reviewed` with its reason, so an absent finding never reads as a false fix. A bad baseline refuses with a typed code (`missing-compare-bundle`, `unsafe-compare-bundle`, `corrupt-compare-bundle`, `unrecorded-compare-bundle`, `bad-compare`). `--json` adds `data.compare`, and the Go SDK, the MCP `report` tool and the TypeScript client carry the flag. The comparison is advisory and changes no exit code.
