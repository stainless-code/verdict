---
"@stainless-code/verdict": patch
---

Experimental opt-in Go implementation of `post`. `VERDICT_IMPL=go`
dispatches the verb to the compiled binary (`VERDICT_BIN`, else
`bin/verdict` from `make build`) reusing the proven record core — anchor
preflight, binding-verdict gating, the single-write finalizer, findings
merge, and submit — with byte-identical envelopes, hints, exits, and every
written file (the parity harness compares stdout, stderr, exit, all files,
and the exact `gh` calls). Patch, not minor: user-facing behavior is
unchanged unless the experimental switch is set. The Go leg additionally
retries secondary rate limits with bounded backoff and trims bodies past the
hosting limit with a notice (graded by a Go-only eval until ticket 08).
