"@stainless-code/verdict": patch
---

Experimental opt-in Go implementation of `record`. `VERDICT_IMPL=go`
dispatches the `record` verb to a compiled binary (`VERDICT_BIN`, else
`bin/verdict` from `make build`) with byte-identical envelopes, hints, and
exits; the default stays shell, a missing binary refuses typed
`missing-go-binary`, and unknown switch values are usage errors. Patch, not
minor: user-facing behavior is unchanged unless the experimental switch is
set. Only `record` dispatches — later verbs follow the same switch.
