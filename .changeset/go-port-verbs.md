---
"@stainless-code/verdict": minor
---

Experimental opt-in Go implementations of `triage`, `adopt`, `fix`, `learn`, `init`, `install`, and `verify`. `VERDICT_IMPL=go` dispatches these verbs to the compiled binary (`VERDICT_BIN`, else `bin/verdict` from `make build`) with byte-identical envelopes, hints, and exits; the default stays shell, a missing binary refuses typed `missing-go-binary`, and unknown switch values are usage errors. Minor, not patch: six new verbs (plus `verify`) follow the `record` dispatch precedent, and the parity harness (`internal/parity`, per-verb shell-parity suites, the adversarial differential and corpus suites) proves byte parity — exit codes, stdout/stderr, and every written file.
