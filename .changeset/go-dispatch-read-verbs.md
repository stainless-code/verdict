"@stainless-code/verdict": patch
---

Experimental opt-in Go implementations of the pure-read verbs. `VERDICT_IMPL=go`
now also dispatches `report`, `next`, `show`, `ready`, `trend`, `calibrate`,
`doctor`, and `check-tree` to the compiled binary with byte-identical
envelopes, hints, and exits; the default stays shell, a missing binary
refuses typed `missing-go-binary`, and unknown switch values are usage
errors. Patch, not minor: user-facing behavior is unchanged unless the
experimental switch is set.
